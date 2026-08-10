package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// tflogLogger adapts tflog to the library's Logger output port. This is the
// whole reason the library defines a port instead of importing tflog: three
// lines here keep every Terraform package out of the library's import graph.
type tflogLogger struct{}

func (tflogLogger) Debug(ctx context.Context, msg string, kv ...any) {
	tflog.Debug(ctx, msg, fieldsFromKV(kv))
}

// fieldsFromKV converts the port's variadic key/value pairs into tflog's map.
// A trailing key with no value is kept, so a malformed call still logs.
func fieldsFromKV(kv []any) map[string]any {
	fields := make(map[string]any, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok {
			continue
		}
		if i+1 < len(kv) {
			fields[key] = kv[i+1]
			continue
		}
		fields[key] = nil
	}
	return fields
}
