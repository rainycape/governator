package main

// #include <sys/types.h>
// #include <grp.h>
// #include <stdlib.h>
import "C"

import (
	"sync"
	"unsafe"
)

var groupMutex sync.Mutex

// getGroupId returns the gid for the
// given group, or -1 if it does not
// exists.
func getGroupId(name string) int {
	groupMutex.Lock()
	defer groupMutex.Unlock()
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	group := C.getgrnam(cname)
	if group == nil {
		return -1
	}
	return int(group.gr_gid)
}
