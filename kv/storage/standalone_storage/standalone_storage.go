package standalone_storage

import (
	"github.com/Connor1996/badger"
	"github.com/pingcap-incubator/tinykv/kv/config"
	"github.com/pingcap-incubator/tinykv/kv/storage"
	"github.com/pingcap-incubator/tinykv/kv/util/engine_util"
	"github.com/pingcap-incubator/tinykv/proto/pkg/kvrpcpb"
)

var (
	ETAG = " | [ERROR] | standalone_storage: "
	FTAG = " | [FAILED] | standalone_storage: "
	DTAG = " | [DEBUG] | standalone_storage: "
)

// StandAloneStorage is an implementation of `Storage` for a single-node TinyKV instance. It does not
// communicate with other nodes and all data is stored locally.
type StandAloneStorage struct {
	// Your Data Here (1).
	DB		*badger.DB
}

type StandAloneStorageReader struct {
	Storage	*StandAloneStorage
	Txn		*badger.Txn
}

func (reader *StandAloneStorageReader)GetCF(cf string, key []byte) ([]byte, error) {
	return engine_util.GetCF(reader.Storage.DB, cf, key)
}

func (reader *StandAloneStorageReader)IterCF(cf string) engine_util.DBIterator {
	itor := engine_util.NewCFIterator(cf, reader.Txn)
	return itor
}

func (reader *StandAloneStorageReader)Close() {
	if reader.Txn != nil {
		reader.Txn.Commit()
	}
	if reader.Storage != nil {
		reader.Storage.DB.Close()
	}
}

func NewStandAloneStorage(conf *config.Config) *StandAloneStorage {
	// Your Code Here (1).
	sas := StandAloneStorage{}
	db := engine_util.CreateDB(conf.DBPath, conf.Raft)
	sas.DB = db
	return &sas
}

func (s *StandAloneStorage) Start() error {
	// Your Code Here (1).
	return nil
}

func (s *StandAloneStorage) Stop() error {
	// Your Code Here (1).
	if s.DB != nil {
		s.DB.Close()
	}
	return nil
}

func (s *StandAloneStorage) Reader(ctx *kvrpcpb.Context) (storage.StorageReader, error) {
	// Your Code Here (1).
	reader := StandAloneStorageReader{s, s.DB.NewTransaction(false)}
	return &reader, nil
}

func (s *StandAloneStorage) Write(ctx *kvrpcpb.Context, batch []storage.Modify) error {
	// Your Code Here (1).
	var err error
	wb := engine_util.WriteBatch{}
	for _, b := range batch {
		wb.SetCF(b.Cf(), b.Key(), b.Value())
	}
	err = wb.WriteToDB(s.DB)
	return err
}
