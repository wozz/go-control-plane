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
	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/ptypes"

	envoy_api_v2 "github.com/envoyproxy/go-control-plane/envoy/api/v2"
	envoy_config_cluster_v3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	envoy_config_endpoint_v3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	hcm "github.com/envoyproxy/go-control-plane/envoy/config/filter/network/http_connection_manager/v2"
	envoy_config_listener_v3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	hcm_v3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	envoy_service_discovery_v2 "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v2"
	"github.com/envoyproxy/go-control-plane/pkg/apiversions"
	"github.com/envoyproxy/go-control-plane/pkg/conversion"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
)

// Resource is the base interface for the xDS payload.
type Resource interface {
	proto.Message
}

type DiscoveryType [apiversions.UnknownVersion]string

func (dt DiscoveryType) Includes(typeURL string) bool {
	for _, url := range dt {
		if url == typeURL {
			return true
		}
	}
	return false
}

// Resource types in xDS
var (
	apiTypePrefix = "type.googleapis.com/"

	EndpointType = DiscoveryType{
		apiTypePrefix + "envoy.api.v2.ClusterLoadAssignment",
		apiTypePrefix + "envoy.config.endpoint.v3.ClusterLoadAssignment",
	}
	ClusterType = DiscoveryType{
		apiTypePrefix + "envoy.api.v2.Cluster",
		apiTypePrefix + "envoy.config.cluster.v3.Cluster",
	}
	RouteType = DiscoveryType{
		apiTypePrefix + "envoy.api.v2.RouteConfiguration",
		apiTypePrefix + "envoy.config.route.v3.RouteConfiguration",
	}
	ListenerType = DiscoveryType{
		apiTypePrefix + "envoy.api.v2.Listener",
		apiTypePrefix + "envoy.config.listener.v3.Listener",
	}
	SecretType = DiscoveryType{
		apiTypePrefix + "envoy.api.v2.auth.Secret",
		apiTypePrefix + "envoy.extensions.transport_sockets.tls.v3.Secret",
	}
	RuntimeType = DiscoveryType{
		apiTypePrefix + "envoy.service.discovery.v2.Runtime",
		apiTypePrefix + "envoy.service.runtime.v3.Runtime",
	}

	// AnyType is used only by ADS
	AnyType = ""
)

// ResponseType enumeration of supported response types
type ResponseType int

const (
	Endpoint ResponseType = iota
	Cluster
	Route
	Listener
	Secret
	Runtime
	UnknownType // token to count the total number of supported types
)

// GetResponseType returns the enumeration for a valid xDS type URL
func GetResponseType(typeURL string) ResponseType {
	switch typeURL {
	case EndpointType[apiversions.V2]:
		return Endpoint
	case ClusterType[apiversions.V2]:
		return Cluster
	case RouteType[apiversions.V2]:
		return Route
	case ListenerType[apiversions.V2]:
		return Listener
	case SecretType[apiversions.V2]:
		return Secret
	case RuntimeType[apiversions.V2]:
		return Runtime
	case EndpointType[apiversions.V3]:
		return Endpoint
	case ClusterType[apiversions.V3]:
		return Cluster
	case RouteType[apiversions.V3]:
		return Route
	case ListenerType[apiversions.V3]:
		return Listener
	case SecretType[apiversions.V3]:
		return Secret
	case RuntimeType[apiversions.V3]:
		return Runtime
	}
	return UnknownType
}

func getAPIVersion(typeURL string) apiversions.APIVersion {
	getVersion := func(d DiscoveryType) apiversions.APIVersion {
		for apiVersion, url := range d {
			if url == typeURL {
				return apiversions.APIVersion(apiVersion)
			}
		}
		return apiversions.UnknownVersion
	}
	switch {
	case EndpointType.Includes(typeURL):
		return getVersion(EndpointType)
	case ClusterType.Includes(typeURL):
		return getVersion(ClusterType)
	case RouteType.Includes(typeURL):
		return getVersion(RouteType)
	case ListenerType.Includes(typeURL):
		return getVersion(ListenerType)
	case SecretType.Includes(typeURL):
		return getVersion(SecretType)
	case RuntimeType.Includes(typeURL):
		return getVersion(RuntimeType)
	}
	return apiversions.UnknownVersion
}

type defaultNameGetter interface {
	GetName() string
}

// GetResourceName returns the resource name for a valid xDS response type.
func GetResourceName(res Resource) string {
	switch v := res.(type) {
	case *envoy_api_v2.ClusterLoadAssignment:
		return v.GetClusterName()
	case *envoy_config_endpoint_v3.ClusterLoadAssignment:
		return v.GetClusterName()
	// other types all use a common name function:
	case defaultNameGetter:
		return v.GetName()
	default:
		return ""
	}
}

// MarshalResource converts the Resource to MarshaledResource
func MarshalResource(resource Resource) (MarshaledResource, error) {
	b := proto.NewBuffer(nil)
	b.SetDeterministic(true)
	err := b.Marshal(resource)
	if err != nil {
		return nil, err
	}

	return b.Bytes(), nil
}

// GetResourceReferences returns the names for dependent resources (EDS cluster
// names for CDS, RDS routes names for LDS).
func GetResourceReferences(resources map[string]Resource) map[string]bool {
	out := make(map[string]bool)
	for _, res := range resources {
		if res == nil {
			continue
		}
		switch v := res.(type) {
		case *envoy_api_v2.ClusterLoadAssignment:
			// no dependencies
		case *envoy_api_v2.Cluster:
			// for EDS type, use cluster name or ServiceName override
			switch typ := v.ClusterDiscoveryType.(type) {
			case *envoy_api_v2.Cluster_Type:
				if typ.Type == envoy_api_v2.Cluster_EDS {
					if v.GetEdsClusterConfig().GetServiceName() != "" {
						out[v.GetEdsClusterConfig().GetServiceName()] = true
					} else {
						out[v.GetName()] = true
					}
				}
			}
		case *envoy_api_v2.RouteConfiguration:
			// References to clusters in both routes (and listeners) are not included
			// in the result, because the clusters are retrieved in bulk currently,
			// and not by name.
		case *envoy_api_v2.Listener:
			// extract route configuration names from HTTP connection manager
			for _, chain := range v.FilterChains {
				for _, filter := range chain.Filters {
					if filter.Name != wellknown.HTTPConnectionManager {
						continue
					}

					config := &hcm.HttpConnectionManager{}

					// use typed config if available
					if typedConfig := filter.GetTypedConfig(); typedConfig != nil {
						ptypes.UnmarshalAny(typedConfig, config)
					} else {
						conversion.StructToMessage(filter.GetConfig(), config)
					}

					if config == nil {
						continue
					}

					if rds, ok := config.RouteSpecifier.(*hcm.HttpConnectionManager_Rds); ok && rds != nil && rds.Rds != nil {
						out[rds.Rds.RouteConfigName] = true
					}
				}
			}
		case *envoy_service_discovery_v2.Runtime:
			// no dependencies
		// no dependencies for v3 ClusterLoadAssignment, RouteConfiguration, Secret, Runtime
		case *envoy_config_cluster_v3.Cluster:
			// for EDS type, use cluster name or ServiceName override
			switch typ := v.ClusterDiscoveryType.(type) {
			case *envoy_config_cluster_v3.Cluster_Type:
				if typ.Type == envoy_config_cluster_v3.Cluster_EDS {
					if v.GetEdsClusterConfig().GetServiceName() != "" {
						out[v.GetEdsClusterConfig().GetServiceName()] = true
					} else {
						out[v.GetName()] = true
					}
				}
			}
		case *envoy_config_listener_v3.Listener:
			// extract route configuration names from HTTP connection manager
			for _, chain := range v.GetFilterChains() {
				for _, filter := range chain.GetFilters() {
					if filter.GetName() != wellknown.HTTPConnectionManager {
						continue
					}

					config := &hcm_v3.HttpConnectionManager{}

					if typedConfig := filter.GetTypedConfig(); typedConfig != nil {
						ptypes.UnmarshalAny(typedConfig, config)
					} else {
						continue
					}

					if config == nil {
						continue
					}

					if rds, ok := config.RouteSpecifier.(*hcm_v3.HttpConnectionManager_Rds); ok && rds != nil && rds.Rds != nil {
						out[rds.Rds.GetRouteConfigName()] = true
					}
				}
			}
		}
	}
	return out
}
