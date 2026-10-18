package bgs

import (
	"fmt"
	"unicode"

	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// FormatMessage renders a message on one line for logs. Binary bytes fields
// (keys, tokens) are replaced by "<N bytes>"; printable ones are kept.
func FormatMessage(m proto.Message) string {
	m = proto.Clone(m)
	summarizeBytes(m.ProtoReflect())
	return prototext.MarshalOptions{}.Format(m)
}

func summarizeBytes(m protoreflect.Message) {
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.Kind() == protoreflect.BytesKind && !fd.IsList():
			if b := v.Bytes(); !printable(b) {
				m.Set(fd, protoreflect.ValueOfBytes([]byte(fmt.Sprintf("<%d bytes>", len(b)))))
			}
		case fd.Kind() == protoreflect.MessageKind && fd.IsList():
			list := v.List()
			for i := 0; i < list.Len(); i++ {
				summarizeBytes(list.Get(i).Message())
			}
		case fd.Kind() == protoreflect.MessageKind && !fd.IsMap():
			summarizeBytes(v.Message())
		}
		return true
	})
}

func printable(b []byte) bool {
	for _, r := range string(b) {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}
