package server

import (
	"context"
	"log"

	"github.com/pingcap-incubator/tinykv/kv/storage"
	"github.com/pingcap-incubator/tinykv/proto/pkg/kvrpcpb"
)

var(
	ETAG = " | [ERROR] | server: "
	FTAG = " | [FAILED] | server: "
	DTAG = " | [DEBUG] | server: "
)

// The functions below are Server's Raw API. (implements TinyKvServer).
// Some helper methods can be found in sever.go in the current directory

// RawGet return the corresponding Get response based on RawGetRequest's CF and Key fields
func (server *Server) RawGet(_ context.Context, req *kvrpcpb.RawGetRequest) (*kvrpcpb.RawGetResponse, error) {
	// Your Code Here (1).
	var resp kvrpcpb.RawGetResponse
	reader, err := server.storage.Reader(nil)
	if err != nil {
		log.Println(ETAG, "fail to get reader:", err)
		return nil, err
	}
	ans, err := reader.GetCF(req.GetCf(), req.Key)
	if ans == nil {
		resp = kvrpcpb.RawGetResponse{NotFound: true}
	} else {
		resp = kvrpcpb.RawGetResponse{Value: ans}
	}
	reader.Close()
	log.Println(DTAG, "[RawGet]:", resp)
	return &resp, nil
}

// RawPut puts the target data into storage and returns the corresponding response
func (server *Server) RawPut(_ context.Context, req *kvrpcpb.RawPutRequest) (*kvrpcpb.RawPutResponse, error) {
	// Your Code Here (1).
	// Hint: Consider using Storage.Modify to store data to be modified
	var ms = []storage.Modify{
		storage.Modify{
			storage.Put{
				Key: req.Key,
				Value: req.Value,
				Cf: req.Cf},
			},
		}
	var err error
	err = server.storage.Write(nil, ms)
	return nil, err
}

// RawDelete delete the target data from storage and returns the corresponding response
func (server *Server) RawDelete(_ context.Context, req *kvrpcpb.RawDeleteRequest) (*kvrpcpb.RawDeleteResponse, error) {
	// Your Code Here (1).
	// Hint: Consider using Storage.Modify to store data to be deleted
	var err error
	var m = []storage.Modify {
		storage.Modify{
			storage.Delete{
				Key: req.Key,
				Cf: req.Cf,
			},
		},
	}
	err = server.storage.Write(nil, m)
	return nil, err
}

// RawScan scan the data starting from the start key up to limit. and return the corresponding result
func (server *Server) RawScan(_ context.Context, req *kvrpcpb.RawScanRequest) (*kvrpcpb.RawScanResponse, error) {
	// Your Code Here (1).
	// Hint: Consider using reader.IterCF
	reader, err := server.storage.Reader(nil)
	if err != nil {
		log.Fatalln(ETAG, "open db reader failed:", err)
	}
	var kvs []*kvrpcpb.KvPair
	iterator := reader.IterCF(req.Cf)
	for ; iterator.Valid(); iterator.Next() {
		it := iterator.Item()
		key := it.Key()
		value, _ := it.Value()
		kv := kvrpcpb.KvPair{Key: key, Value: value}
		kvs = append(kvs, &kv)
	}
	var resp kvrpcpb.RawScanResponse
	resp.Kvs = kvs
	return &resp, err
}
