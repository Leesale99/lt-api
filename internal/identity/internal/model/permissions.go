package model

import "slices"

// Permissions is a role's resolved permission codes.
type Permissions []string

// Include reports whether the set carries code.
func (p Permissions) Include(code string) bool {
	return slices.Contains(p, code)
}
