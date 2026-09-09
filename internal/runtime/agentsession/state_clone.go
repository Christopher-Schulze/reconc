package agentsession

import "reflect"

// cloneSessionState isolates callback input from the loaded baseline. Session
// mutations are compared against that baseline after the callback returns, so
// every mutable field must have independent storage before arbitrary callback
// code runs.
func cloneSessionState(state SessionState) SessionState {
	state.ReadPaths = cloneSessionStrings(state.ReadPaths)
	state.WritePaths = cloneSessionStrings(state.WritePaths)
	state.WriteEpochs = cloneSessionUint64Map(state.WriteEpochs)
	state.Commands = cloneSessionStrings(state.Commands)
	state.Claims = cloneSessionStrings(state.Claims)
	state.CommandResults = cloneSessionCommandResults(state.CommandResults)
	state.PendingToolCalls = cloneSessionPendingToolCalls(state.PendingToolCalls)
	state.RetiredToolCallKeys = cloneSessionInt64Map(state.RetiredToolCallKeys)
	state.AntigravityStepHighWater = cloneSessionUint64Pointer(state.AntigravityStepHighWater)
	state.ConsumedApprovalIdentities = cloneSessionStrings(state.ConsumedApprovalIdentities)
	return state
}

func cloneSessionStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

func cloneSessionUint64Map(values map[string]uint64) map[string]uint64 {
	if values == nil {
		return nil
	}
	out := make(map[string]uint64, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneSessionInt64Map(values map[string]int64) map[string]int64 {
	if values == nil {
		return nil
	}
	out := make(map[string]int64, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneSessionCommandResults(values []CommandResult) []CommandResult {
	if values == nil {
		return nil
	}
	out := make([]CommandResult, len(values))
	copy(out, values)
	for index := range out {
		out[index].ExitCode = cloneSessionIntPointer(values[index].ExitCode)
		out[index].IsInterrupt = cloneSessionBoolPointer(values[index].IsInterrupt)
	}
	return out
}

func cloneSessionPendingToolCalls(values map[string]PendingToolCall) map[string]PendingToolCall {
	if values == nil {
		return nil
	}
	out := make(map[string]PendingToolCall, len(values))
	for key, value := range values {
		value.ToolInput = cloneSessionToolInput(value.ToolInput)
		out[key] = value
	}
	return out
}

func cloneSessionToolInput(values map[string]interface{}) map[string]interface{} {
	if values == nil {
		return nil
	}
	out := make(map[string]interface{}, len(values))
	for key, value := range values {
		out[key] = cloneSessionValue(reflect.ValueOf(value))
	}
	return out
}

func cloneSessionValue(value reflect.Value) interface{} {
	if !value.IsValid() {
		return nil
	}
	return cloneSessionReflect(value).Interface()
}

func cloneSessionReflect(value reflect.Value) reflect.Value {
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := cloneSessionReflect(value.Elem())
		out := reflect.New(value.Type()).Elem()
		if cloned.IsValid() && cloned.Type().Implements(value.Type()) {
			out.Set(cloned)
			return out
		}
		return value
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.MakeMapWithSize(value.Type(), value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			key := cloneSessionReflect(iterator.Key())
			entry := cloneSessionReflect(iterator.Value())
			out.SetMapIndex(key, entry)
		}
		return out
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for index := 0; index < value.Len(); index++ {
			out.Index(index).Set(cloneSessionReflect(value.Index(index)))
		}
		return out
	case reflect.Array:
		out := reflect.New(value.Type()).Elem()
		for index := 0; index < value.Len(); index++ {
			out.Index(index).Set(cloneSessionReflect(value.Index(index)))
		}
		return out
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.New(value.Type().Elem())
		out.Elem().Set(cloneSessionReflect(value.Elem()))
		return out
	case reflect.Struct:
		out := reflect.New(value.Type()).Elem()
		for index := 0; index < value.NumField(); index++ {
			field := out.Field(index)
			if !field.CanSet() {
				return value
			}
			field.Set(cloneSessionReflect(value.Field(index)))
		}
		return out
	default:
		return value
	}
}

func cloneSessionIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneSessionBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneSessionUint64Pointer(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
