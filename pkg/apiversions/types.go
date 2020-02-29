package apiversions

import (
	"github.com/golang/protobuf/proto"
)

type DiscoveryRequest interface {
	proto.Message
	GetTypeUrl() string
	GetResponseNonce() string
	GetVersionInfo() string
	GetResourceNames() []string
}

type DiscoveryResponse interface {
	proto.Message
	GetNonce() string
	GetVersionInfo() string
}
