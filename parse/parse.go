package parse

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	matchSelectSQL = `[sS][eE][lL][eE][cC][tT] [\*0-9a-zA-Z\_,\s\(\)\\.]+ [fF][rR][oO][mM] (((([0-9a-zA-Z_]\s+)?([0-9a-zA-Z_]+))\s*)(,\s*(([0-9a-zA-Z_]\s+)?([0-9a-zA-Z_]+))\s*)*)`
	matchInsertSQL = `[iI][nN][sS][eE][rR][tT] [iI][nN][tT][oO] (((([0-9a-zA-Z_]\s+)?([0-9a-zA-Z_]+))\s*)(,\s*(([0-9a-zA-Z_]\s+)?([0-9a-zA-Z_]+))\s*)*)`
	matchDeleteSQL = `[dD][eE][lL][eE][tT][eE] [uUpPdDaAtT] (((([0-9a-zA-Z_]\s+)?([0-9a-zA-Z_]+))\s*)(,\s*(([0-9a-zA-Z_]\s+)?([0-9a-zA-Z_]+))\s*)*)`
	matchUpdateSQL = `[uU][pP][dD][aA][tT] (((([0-9a-zA-Z_]\s+)?([0-9a-zA-Z_]+))\s*)(,\s*(([0-9a-zA-Z_]\s+)?([0-9a-zA-Z_]+))\s*)*)`

	regexSelectSQL *regexp.Regexp
	regexInsertSQL *regexp.Regexp
	regexDeleteSQL *regexp.Regexp
	regexUpdateSQL *regexp.Regexp
)

func init() {
	regexSelectSQL = regexp.MustCompile(matchSelectSQL)
	regexInsertSQL = regexp.MustCompile(matchInsertSQL)
	regexDeleteSQL = regexp.MustCompile(matchDeleteSQL)
	regexUpdateSQL = regexp.MustCompile(matchUpdateSQL)
}

type Generator struct {
	Functions  map[string]*function // 存储函数信息
	Table2Func map[string]*Set      // 存储表到函数的映射关系
	Vals       map[string]string    // 存储定义在函数外的sql
}

func Parse(fpath string) (*Generator, error) {
	g := &Generator{
		Functions:  make(map[string]*function),
		Table2Func: make(map[string]*Set),
		Vals:       make(map[string]string),
	}

	err := filepath.WalkDir(fpath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			panic(err)
		}

		if d.IsDir() {
			return nil
		}

		filename := d.Name()
		if !strings.HasSuffix(filename, ".go") {
			return nil
		}

		if strings.HasSuffix(filename, "_test.go") {
			return nil
		}

		code, err := ReadFile(path)
		if err != nil {
			return err
		}

		vvs := parseValues(code)
		for k, v := range vvs {
			if _, ok := g.Vals[k]; !ok {
				g.Vals[k] = v
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	err = filepath.WalkDir(fpath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			panic(err)
		}

		if d.IsDir() {
			return nil
		}

		filename := d.Name()
		if !strings.HasSuffix(filename, ".go") {
			return nil
		}

		if strings.HasSuffix(filename, "_test.go") {
			return nil
		}

		code, err := ReadFile(path)
		if err != nil {
			return err
		}

		functions, err := g.parseFunctions(path, code)
		if err != nil {
			return err
		}

		for _, v := range functions {
			g.Functions[v.Name] = v
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	g.tag()
	for name, f := range g.Functions {
		if !f.isInvoked {
			g.propagation(name)
		}
	}

	return g, nil
}

func (g *Generator) tag() {
	var dfs func(cur string, invoke bool)
	dfs = func(cur string, invoke bool) {
		if _, ok := g.Functions[cur]; !ok {
			return
		}
		if g.Functions[cur].isInvoked {
			return
		}

		g.Functions[cur].isInvoked = invoke
		g.Functions[cur].Invoked.Walk(func(v string) bool {
			if _, ok := g.Functions[v]; ok {
				dfs(v, true)
			}
			return true
		})
	}

	for name, f := range g.Functions {
		if f.isInvoked {
			continue
		}
		dfs(name, false)
	}
}

func (g *Generator) propagation(cur string) *Set {
	isLeaf := true
	if _, ok := g.Functions[cur]; !ok {
		return nil
	}
	if g.Functions[cur].isVisited {
		return g.Functions[cur].TableUsed
	}

	g.Functions[cur].Invoked.Walk(func(v string) bool {
		if _, ok := g.Functions[v]; ok {
			isLeaf = false
			return false
		}

		return true
	})

	g.Functions[cur].isVisited = true

	if isLeaf {
		g.Functions[cur].TableUsed.Walk(func(v string) bool {
			if _, ok := g.Table2Func[v]; !ok {
				g.Table2Func[v] = newSet()
			}
			g.Table2Func[v].add(cur)
			return true
		})
		return g.Functions[cur].TableUsed
	}

	// 函数a调用函数b，那么函数b用的表也是函数a用的表
	for v := range g.Functions[cur].Invoked.m {
		if _, ok := g.Functions[v]; !ok {
			continue
		}
		g.Functions[cur].TableUsed.merge(g.propagation(v))
	}

	// 添加表名到函数名的映射关系
	g.Functions[cur].TableUsed.Walk(func(v string) bool {
		if _, ok := g.Table2Func[v]; !ok {
			g.Table2Func[v] = newSet()
		}
		g.Table2Func[v].add(cur)
		return true
	})
	return g.Functions[cur].TableUsed
}

func parseValues(code string) map[string]string {
	variableValues := make(map[string]string)

	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "", code, parser.AllErrors)
	if err != nil {
		return nil
	}

	// 遍历 AST，记录函数外部变量定义及值
	for _, decl := range node.Decls {
		var (
			genDecl *ast.GenDecl
			ok      bool
		)
		if genDecl, ok = decl.(*ast.GenDecl); !ok {
			continue
		}

		if (genDecl.Tok != token.VAR) && (genDecl.Tok != token.CONST) {
			continue
		}

		for _, spec := range genDecl.Specs {
			var valueSpec *ast.ValueSpec
			if valueSpec, ok = spec.(*ast.ValueSpec); !ok {
				continue
			}

			for i, name := range valueSpec.Names {
				if valueSpec.Values != nil && i < len(valueSpec.Values) && valueSpec.Values[i] != nil {
					// 记录变量名及其值
					val := getValue(valueSpec.Values[i])
					if regexSelectSQL.Match([]byte(val)) {
						variableValues[name.Name] = val
					}
				}
			}
		}
	}

	return variableValues
}

func (g *Generator) parseFunctions(path, code string) ([]*function, error) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "", code, parser.AllErrors)
	if err != nil {
		return nil, err
	}

	var functions []*function
	for _, decl := range node.Decls {
		if f, ok := decl.(*ast.FuncDecl); ok {
			fn := &function{
				Name:      node.Name.Name + "/" + f.Name.Name,
				Path:      path,
				isVisited: false,
				isInvoked: false,
				TableUsed: newSet(),
				Invoked:   newSet(),
			}

			ast.Inspect(f.Body, func(x ast.Node) bool {
				switch n := x.(type) {
				case *ast.CallExpr:
					if ident, ok := n.Fun.(*ast.Ident); ok {
						fn.Invoked.add(node.Name.Name + "/" + ident.Name)
					}
					// 检查是否是带有包名的函数调用
					call := x.(*ast.CallExpr)
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
						if pkgName, ok := sel.X.(*ast.Ident); ok && pkgName.Obj == nil {
							fn.Invoked.add(pkgName.Name + "/" + sel.Sel.Name)
						}
					}

				// 检查函数内部是否使用外部定义的sql语句
				case *ast.Ident:
					ident := x.(*ast.Ident)
					if value, exists := g.Vals[ident.Name]; exists {
						fn.TableUsed.merge(parseTableUsed(value))
					}
				}
				return true
			})

			st := fset.Position(f.Pos())
			ed := fset.Position(f.End())
			tables := parseTableUsed(code[st.Offset:ed.Offset])
			if tables != nil && tables.Len() != 0 {
				fn.TableUsed.merge(tables)
			}

			functions = append(functions, fn)
		}
	}

	return functions, nil
}

func parseTableUsed(s string) *Set {
	res := newSet()

	fileds := strings.Fields(s)
	for _, filed := range fileds {
		if strings.HasPrefix(filed, "t_") {
			lastLegal := len(filed)
			for idx, ch := range filed {
				if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_') {
					lastLegal = idx
					break
				}
			}
			res.add(filed[:lastLegal])
		}
	}

	return res
}

func ReadFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}

	data, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

// 获取表达式的值
func getValue(expr ast.Expr) string {
	if basicLit, ok := expr.(*ast.BasicLit); ok {
		return strings.Trim(basicLit.Value, "\"")
	}
	return ""
}
