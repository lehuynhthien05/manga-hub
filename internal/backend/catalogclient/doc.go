// Package catalogclient is the backend's gRPC client for the catalog service
// and implements api.Catalog. It applies GRPC_TIMEOUT to every call and is
// the only way catalog data reaches the backend; it must not import internal/catalog.
package catalogclient
