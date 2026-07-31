package annotations

import (
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// Field numbers matching annotations.proto extensions.
const (
	fieldContextField  protowire.Number = 52103
	rpcToolField       protowire.Number = 52104
	fieldBehaviorField protowire.Number = 1052
)

// ToolDefinition mirrors the proto ToolDefinition message.
type ToolDefinition struct {
	Name        string
	Description string
	Strict      bool
	AutoExecute bool
}

// ToolFromMethod extracts the ToolDefinition annotation from a method.
func ToolFromMethod(method protoreflect.MethodDescriptor) (ToolDefinition, bool) {
	opts, ok := method.Options().(*descriptorpb.MethodOptions)
	if !ok || opts == nil {
		return ToolDefinition{}, false
	}
	raw := opts.ProtoReflect().GetUnknown()
	ext := findExtension(raw, rpcToolField)
	if ext == nil {
		return ToolDefinition{}, false
	}
	return parseToolDefinition(ext), true
}

// FieldDescription extracts the description from a field_context annotation.
func FieldDescription(field protoreflect.FieldDescriptor) string {
	opts, ok := field.Options().(*descriptorpb.FieldOptions)
	if !ok || opts == nil {
		return ""
	}
	raw := opts.ProtoReflect().GetUnknown()
	ext := findExtension(raw, fieldContextField)
	if ext == nil {
		return ""
	}
	return parseFieldContextDescription(ext)
}

// toolFieldNumber is the extension field for FieldSkip (ai.tools.v1.tool_field).
const toolFieldNumber protowire.Number = 52105

// IsToolSkipped checks if a field has (ai.tools.v1.tool_field).skip = true.
// Uses pure wire format parsing to avoid importing generated types (which
// conflicts with the proto registry when running as a protoc plugin).
func IsToolSkipped(field protoreflect.FieldDescriptor) bool {
	opts, ok := field.Options().(*descriptorpb.FieldOptions)
	if !ok || opts == nil {
		return false
	}
	raw := opts.ProtoReflect().GetUnknown()
	ext := findExtension(raw, toolFieldNumber)
	if ext == nil {
		return false
	}
	// Parse FieldSkip message: field 1 = skip (varint/bool)
	for len(ext) > 0 {
		num, typ, n := protowire.ConsumeTag(ext)
		if n < 0 {
			return false
		}
		ext = ext[n:]
		if typ == protowire.VarintType {
			v, m := protowire.ConsumeVarint(ext)
			if m < 0 {
				return false
			}
			if num == 1 {
				return v > 0
			}
			ext = ext[m:]
		} else {
			skip := consumeField(typ, ext)
			if skip < 0 {
				return false
			}
			ext = ext[skip:]
		}
	}
	return false
}

// IsOutputOnly checks if a field has OUTPUT_ONLY field_behavior.
// google.api.field_behavior is field 1052 on FieldOptions, a repeated enum.
// Value 3 = OUTPUT_ONLY.
func IsOutputOnly(field protoreflect.FieldDescriptor) bool {
	opts, ok := field.Options().(*descriptorpb.FieldOptions)
	if !ok || opts == nil {
		return false
	}
	raw := opts.ProtoReflect().GetUnknown()
	return hasFieldBehaviorValue(raw, 3)
}

// --- Wire format parsing ---

// findExtension scans unknown fields for a length-delimited extension with the
// given field number and returns its content bytes.
func findExtension(unknown []byte, fieldNumber protowire.Number) []byte {
	for len(unknown) > 0 {
		num, typ, n := protowire.ConsumeTag(unknown)
		if n < 0 {
			return nil
		}
		unknown = unknown[n:]

		switch typ {
		case protowire.VarintType:
			_, m := protowire.ConsumeVarint(unknown)
			if m < 0 {
				return nil
			}
			unknown = unknown[m:]
		case protowire.Fixed32Type:
			_, m := protowire.ConsumeFixed32(unknown)
			if m < 0 {
				return nil
			}
			unknown = unknown[m:]
		case protowire.Fixed64Type:
			_, m := protowire.ConsumeFixed64(unknown)
			if m < 0 {
				return nil
			}
			unknown = unknown[m:]
		case protowire.BytesType:
			b, m := protowire.ConsumeBytes(unknown)
			if m < 0 {
				return nil
			}
			if num == fieldNumber {
				return b
			}
			unknown = unknown[m:]
		default:
			return nil
		}
	}
	return nil
}

// hasFieldBehaviorValue scans unknown fields for repeated varint entries at
// field number 1052 (google.api.field_behavior) and returns true if any matches
// the target value.
func hasFieldBehaviorValue(unknown []byte, target uint64) bool {
	for len(unknown) > 0 {
		num, typ, n := protowire.ConsumeTag(unknown)
		if n < 0 {
			return false
		}
		unknown = unknown[n:]

		switch typ {
		case protowire.VarintType:
			v, m := protowire.ConsumeVarint(unknown)
			if m < 0 {
				return false
			}
			if num == fieldBehaviorField && v == target {
				return true
			}
			unknown = unknown[m:]
		case protowire.Fixed32Type:
			_, m := protowire.ConsumeFixed32(unknown)
			if m < 0 {
				return false
			}
			unknown = unknown[m:]
		case protowire.Fixed64Type:
			_, m := protowire.ConsumeFixed64(unknown)
			if m < 0 {
				return false
			}
			unknown = unknown[m:]
		case protowire.BytesType:
			b, m := protowire.ConsumeBytes(unknown)
			if m < 0 {
				return false
			}
			// field_behavior can also be packed as a bytes field
			if num == fieldBehaviorField {
				// Packed repeated varint — scan the bytes for the target value
				packed := b
				for len(packed) > 0 {
					v, pm := protowire.ConsumeVarint(packed)
					if pm < 0 {
						break
					}
					if v == target {
						return true
					}
					packed = packed[pm:]
				}
			}
			unknown = unknown[m:]
		default:
			return false
		}
	}
	return false
}

func parseToolDefinition(raw []byte) ToolDefinition {
	var out ToolDefinition
	for len(raw) > 0 {
		num, typ, n := protowire.ConsumeTag(raw)
		if n < 0 {
			return out
		}
		raw = raw[n:]

		switch typ {
		case protowire.BytesType:
			b, m := protowire.ConsumeBytes(raw)
			if m < 0 {
				return out
			}
			switch num {
			case 1:
				out.Name = string(b)
			case 2:
				out.Description = string(b)
			}
			raw = raw[m:]
		case protowire.VarintType:
			v, m := protowire.ConsumeVarint(raw)
			if m < 0 {
				return out
			}
			switch num {
			case 3:
				out.Strict = v > 0
			case 4:
				out.AutoExecute = v > 0
			}
			raw = raw[m:]
		default:
			skip := consumeField(typ, raw)
			if skip < 0 {
				return out
			}
			raw = raw[skip:]
		}
	}
	return out
}

// parseFieldContextDescription extracts field 1 (description) from a FieldContext message.
func parseFieldContextDescription(raw []byte) string {
	for len(raw) > 0 {
		num, typ, n := protowire.ConsumeTag(raw)
		if n < 0 {
			return ""
		}
		raw = raw[n:]
		if typ == protowire.BytesType {
			b, m := protowire.ConsumeBytes(raw)
			if m < 0 {
				return ""
			}
			if num == 1 {
				return string(b)
			}
			raw = raw[m:]
		} else {
			skip := consumeField(typ, raw)
			if skip < 0 {
				return ""
			}
			raw = raw[skip:]
		}
	}
	return ""
}

func consumeField(typ protowire.Type, raw []byte) int {
	switch typ {
	case protowire.VarintType:
		_, m := protowire.ConsumeVarint(raw)
		return m
	case protowire.Fixed32Type:
		_, m := protowire.ConsumeFixed32(raw)
		return m
	case protowire.Fixed64Type:
		_, m := protowire.ConsumeFixed64(raw)
		return m
	case protowire.BytesType:
		_, m := protowire.ConsumeBytes(raw)
		return m
	default:
		return -1
	}
}
