//go:build !linux

package nodemods

// Foreign says whether anything in instance's directories belongs to someone
// other than uid. Agents only run on Linux.
func (s *Store) Foreign(instance string, uid int) bool { return false }
