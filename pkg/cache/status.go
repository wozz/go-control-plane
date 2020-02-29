// Copyright 2018 Envoyproxy Authors
//
//   Licensed under the Apache License, Version 2.0 (the "License");
//   you may not use this file except in compliance with the License.
//   You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
//   Unless required by applicable law or agreed to in writing, software
//   distributed under the License is distributed on an "AS IS" BASIS,
//   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//   See the License for the specific language governing permissions and
//   limitations under the License.

package cache

import (
	"sync"
	"time"

	envoy_api_v2 "github.com/envoyproxy/go-control-plane/envoy/api/v2"
	envoy_api_v2_core "github.com/envoyproxy/go-control-plane/envoy/api/v2/core"
	envoy_config_core_v3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	envoy_service_discovery_v3 "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"github.com/envoyproxy/go-control-plane/pkg/apiversions"
)

// NodeHash computes string identifiers for Envoy nodes.
type NodeHash interface {
	// ID function defines a unique string identifier for the remote Envoy node.
	ID(req apiversions.DiscoveryRequest) string
}

// IDHash uses ID field as the node hash.
type IDHash struct{}

// ID uses the node ID field
func (IDHash) ID(req apiversions.DiscoveryRequest) string {
	switch r := req.(type) {
	case *envoy_api_v2.DiscoveryRequest:
		return r.GetNode().GetId()
	case *envoy_service_discovery_v3.DiscoveryRequest:
		return r.GetNode().GetId()
	default:
		return ""
	}
}

var _ NodeHash = IDHash{}

// StatusInfo tracks the server state for the remote Envoy node.
// Not all fields are used by all cache implementations.
type StatusInfo interface {
	// GetNodeV2 returns the v2 node metadata if available
	GetNodeV2() *envoy_api_v2_core.Node

	// GetNodeV3 returns the v3 node metadata if available
	GetNodeV3() *envoy_config_core_v3.Node

	// GetNumWatches returns the number of open watches.
	GetNumWatches() int

	// GetLastWatchRequestTime returns the timestamp of the last discovery watch request.
	GetLastWatchRequestTime() time.Time
}

type statusInfo struct {
	// nodeV2 is the constant Envoy node v2 metadata.
	nodeV2 *envoy_api_v2_core.Node

	// nodeV3 is the constant Envoy node v3 metadata.
	nodeV3 *envoy_config_core_v3.Node

	// watches are indexed channels for the response watches and the original requests.
	watches map[int64]ResponseWatch

	// the timestamp of the last watch request
	lastWatchRequestTime time.Time

	// mutex to protect the status fields.
	// should not acquire mutex of the parent cache after acquiring this mutex.
	mu sync.RWMutex
}

// ResponseWatch is a watch record keeping both the request and an open channel for the response.
type ResponseWatch struct {
	// Request is the original request for the watch.
	Request Request

	// Response is the channel to push response to.
	Response chan Response
}

// newStatusInfo initializes a status info data structure.
func newStatusInfo(req apiversions.DiscoveryRequest) *statusInfo {
	out := statusInfo{
		watches: make(map[int64]ResponseWatch),
	}
	switch r := req.(type) {
	case *envoy_api_v2.DiscoveryRequest:
		out.nodeV2 = r.Node
	case *envoy_service_discovery_v3.DiscoveryRequest:
		out.nodeV3 = r.Node
	}
	return &out
}

func (info *statusInfo) GetNodeV2() *envoy_api_v2_core.Node {
	info.mu.RLock()
	defer info.mu.RUnlock()
	return info.nodeV2
}

func (info *statusInfo) GetNodeV3() *envoy_config_core_v3.Node {
	info.mu.RLock()
	defer info.mu.RUnlock()
	return info.nodeV3
}

func (info *statusInfo) GetNumWatches() int {
	info.mu.RLock()
	defer info.mu.RUnlock()
	return len(info.watches)
}

func (info *statusInfo) GetLastWatchRequestTime() time.Time {
	info.mu.RLock()
	defer info.mu.RUnlock()
	return info.lastWatchRequestTime
}
