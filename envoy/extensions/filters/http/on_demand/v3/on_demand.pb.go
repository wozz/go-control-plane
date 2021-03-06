// Code generated by protoc-gen-go. DO NOT EDIT.
// source: envoy/extensions/filters/http/on_demand/v3/on_demand.proto

package envoy_extensions_filters_http_on_demand_v3

import (
	fmt "fmt"
	_ "github.com/cncf/udpa/go/udpa/annotations"
	_ "github.com/envoyproxy/protoc-gen-validate/validate"
	proto "github.com/golang/protobuf/proto"
	math "math"
)

// Reference imports to suppress errors if they are not otherwise used.
var _ = proto.Marshal
var _ = fmt.Errorf
var _ = math.Inf

// This is a compile-time assertion to ensure that this generated file
// is compatible with the proto package it is being compiled against.
// A compilation error at this line likely means your copy of the
// proto package needs to be updated.
const _ = proto.ProtoPackageIsVersion3 // please upgrade the proto package

type OnDemand struct {
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *OnDemand) Reset()         { *m = OnDemand{} }
func (m *OnDemand) String() string { return proto.CompactTextString(m) }
func (*OnDemand) ProtoMessage()    {}
func (*OnDemand) Descriptor() ([]byte, []int) {
	return fileDescriptor_ea492b8a9902099a, []int{0}
}

func (m *OnDemand) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_OnDemand.Unmarshal(m, b)
}
func (m *OnDemand) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_OnDemand.Marshal(b, m, deterministic)
}
func (m *OnDemand) XXX_Merge(src proto.Message) {
	xxx_messageInfo_OnDemand.Merge(m, src)
}
func (m *OnDemand) XXX_Size() int {
	return xxx_messageInfo_OnDemand.Size(m)
}
func (m *OnDemand) XXX_DiscardUnknown() {
	xxx_messageInfo_OnDemand.DiscardUnknown(m)
}

var xxx_messageInfo_OnDemand proto.InternalMessageInfo

func init() {
	proto.RegisterType((*OnDemand)(nil), "envoy.extensions.filters.http.on_demand.v3.OnDemand")
}

func init() {
	proto.RegisterFile("envoy/extensions/filters/http/on_demand/v3/on_demand.proto", fileDescriptor_ea492b8a9902099a)
}

var fileDescriptor_ea492b8a9902099a = []byte{
	// 201 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0xe2, 0xb2, 0x4a, 0xcd, 0x2b, 0xcb,
	0xaf, 0xd4, 0x4f, 0xad, 0x28, 0x49, 0xcd, 0x2b, 0xce, 0xcc, 0xcf, 0x2b, 0xd6, 0x4f, 0xcb, 0xcc,
	0x29, 0x49, 0x2d, 0x2a, 0xd6, 0xcf, 0x28, 0x29, 0x29, 0xd0, 0xcf, 0xcf, 0x8b, 0x4f, 0x49, 0xcd,
	0x4d, 0xcc, 0x4b, 0xd1, 0x2f, 0x33, 0x46, 0x70, 0xf4, 0x0a, 0x8a, 0xf2, 0x4b, 0xf2, 0x85, 0xb4,
	0xc0, 0x7a, 0xf5, 0x10, 0x7a, 0xf5, 0xa0, 0x7a, 0xf5, 0x40, 0x7a, 0xf5, 0x10, 0xca, 0xcb, 0x8c,
	0xa5, 0x14, 0x4b, 0x53, 0x0a, 0x12, 0xf5, 0x13, 0xf3, 0xf2, 0xf2, 0x4b, 0x12, 0x4b, 0xc0, 0xf6,
	0x94, 0xa5, 0x16, 0x81, 0x34, 0x65, 0xe6, 0xa5, 0x43, 0x8c, 0x93, 0x12, 0x2f, 0x4b, 0xcc, 0xc9,
	0x4c, 0x49, 0x2c, 0x49, 0xd5, 0x87, 0x31, 0x20, 0x12, 0x4a, 0x8e, 0x5c, 0x1c, 0xfe, 0x79, 0x2e,
	0x60, 0xa3, 0xac, 0x4c, 0x67, 0x1d, 0xed, 0x90, 0x33, 0xe0, 0xd2, 0x83, 0x58, 0x9d, 0x9c, 0x9f,
	0x97, 0x96, 0x99, 0x0e, 0xb5, 0x16, 0xc3, 0x56, 0x23, 0x3d, 0x98, 0x36, 0x27, 0x6f, 0x2e, 0x8b,
	0xcc, 0x7c, 0x88, 0xa6, 0x82, 0xa2, 0xfc, 0x8a, 0x4a, 0x3d, 0xe2, 0x9d, 0xee, 0xc4, 0x0b, 0x33,
	0x25, 0x00, 0xe4, 0x9a, 0x00, 0xc6, 0x24, 0x36, 0xb0, 0xb3, 0x8c, 0x01, 0x01, 0x00, 0x00, 0xff,
	0xff, 0x2a, 0x64, 0xcf, 0x08, 0x3c, 0x01, 0x00, 0x00,
}
