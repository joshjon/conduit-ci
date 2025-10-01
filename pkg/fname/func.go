package fname

import (
	"reflect"
	"runtime"
	"strings"
)

// FuncName returns the full function name in the form <prefix>.(*<type>).<function>
func FuncName(fn any) string {
	if fullName, ok := fn.(string); ok {
		return fullName
	}
	fullName := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
	// Compiler adds -fm suffix to a function name which has a receiver.
	return strings.TrimSuffix(fullName, "-fm")
}
