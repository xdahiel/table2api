package parse

type function struct {
	Name      string
	Path      string
	TableUsed *Set
	Invoked   *Set

	isVisited bool
	isInvoked bool
}

type Set struct {
	m map[string]struct{}
}

func newSet() *Set {
	return &Set{m: make(map[string]struct{})}
}

func (st *Set) add(ss ...string) {
	for _, s := range ss {
		if _, ok := st.m[s]; !ok {
			st.m[s] = struct{}{}
		}
	}
}

func (st *Set) merge(t *Set) {
	for k := range t.m {
		if _, ok := st.m[k]; !ok {
			st.m[k] = struct{}{}
		}
	}
}

func (st *Set) Walk(f func(s string) bool) {
	for k := range st.m {
		if !f(k) {
			break
		}
	}
}

func (st *Set) All() []string {
	res := make([]string, 0, len(st.m))
	st.Walk(func(s string) bool {
		res = append(res, s)
		return true
	})
	return res
}

func (st *Set) Len() int {
	return len(st.m)
}
