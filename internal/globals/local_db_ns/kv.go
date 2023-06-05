package local_db_ns

import (
	"sync"

	"github.com/inoxlang/inox/internal/afs"
	core "github.com/inoxlang/inox/internal/core"

	"github.com/inoxlang/inox/internal/utils"
)

const (
	KV_STORE_SRC_NAME = "/kv"
)

// thin wrapper around a buntdb database.
type KVStore struct {
	db   *buntDB
	path core.Path
	host Host

	transactionMapLock sync.Mutex
	transactions       map[*core.Transaction]*Tx
}

func openKvWrapperNoPermCheck(config LocalDatabaseConfig, fls afs.Filesystem) (_ *KVStore, finalErr error) {

	path := string(config.Path)
	if config.InMemory {
		path = ":memory:"
	}

	db, err := openBuntDBNoPermCheck(path, fls)
	if err != nil {
		finalErr = err
		return
	}

	return &KVStore{
		db:   db,
		path: config.Path,
		host: config.Host,

		transactions: map[*core.Transaction]*Tx{},
	}, nil
}

func (kv *KVStore) close(ctx *core.Context) {
	logger := ctx.Logger().With().Str(core.SOURCE_LOG_FIELD_NAME, KV_STORE_SRC_NAME).Logger()
	//before closing the buntdb database all the transactions must be closed or a deadlock will occur.

	logger.Print("close KV store")

	kv.transactionMapLock.Lock()
	transactions := utils.CopyMap(kv.transactions)
	kv.transactionMapLock.Unlock()

	logger.Print("number of transactions to close: ", len(transactions))

	for tx, dbTx := range transactions {
		func() {
			defer recover()
			//will be ignored if the transaction already finished.
			tx.Rollback(ctx)
		}()

		func() {
			defer recover()
			if dbTx.db != nil { //still not finished.
				dbTx.unlock()
			}
		}()
	}

	logger.Print("close bluntDB")
	kv.db.Close()

	if kv.db.isClosed() {
		dbRegistry.lock.Lock()
		defer dbRegistry.lock.Unlock()

		delete(dbRegistry.openDatabases, kv.path)
	}
}

func (kv *KVStore) isClosed() bool {
	return kv.db.isClosed()
}

func (kv *KVStore) get(ctx *Context, key Path, db any) (Value, Bool) {
	if kv.isClosed() {
		panic(errDatabaseClosed)
	}

	if !key.IsAbsolute() {
		panic(ErrInvalidPathKey)
	}

	var (
		r          = getFullResourceName(kv.host, key)
		valueFound = core.True
		val        Value
		tx         = ctx.GetTx()
	)

	if tx == nil {
		err := kv.db.View(func(txn *Tx) error {
			item, err := txn.Get(string(key))
			if err == errNotFound {
				valueFound = core.False
				return nil
			} else if err != nil {
				return err
			}

			val, err = core.ParseRepr(ctx, utils.StringAsBytes(item))
			return err
		})

		if err != nil {
			panic(err)
		}

	} else {
		dbtx := kv.getCreateDatabaseTxn(db, tx)

		item, err := dbtx.Get(string(key))
		if err == errNotFound {
			valueFound = core.False
		} else if err != nil {
			panic(err)
		} else {
			val, err = core.ParseRepr(ctx, utils.StringAsBytes(item))

			if err != nil {
				panic(err)
			}
		}
	}

	if valueFound {
		if err := ctx.AcquireResource(r); err != nil {
			panic(err)
		}
	}

	if val == nil {
		val = core.Nil
	}

	return val, valueFound
}

func (kv *KVStore) getCreateDatabaseTxn(db any, tx *core.Transaction) *Tx {
	//if there is already a database transaction in the core.Transaction we return it.
	v, err := tx.GetValue(db)
	if err != nil {
		panic(err)
	}
	dbTx, ok := v.(*Tx)

	if ok {
		return dbTx
	}

	//begin a new database transaction & add it to the core.Transaction.
	dbTx, err = kv.db.Begin(true)
	if err != nil {
		panic(err)
	}
	if err = tx.SetValue(db, dbTx); err != nil {
		panic(err)
	}

	//add core.Transaction to KV.
	kv.transactionMapLock.Lock()
	kv.transactions[tx] = dbTx
	kv.transactionMapLock.Unlock()

	if err = tx.OnEnd(db, makeTxEndcallbackFn(dbTx, tx, kv)); err != nil {
		panic(err)
	}

	return dbTx
}

func (kv *KVStore) has(ctx *Context, key Path, db any) Bool {
	if kv.isClosed() {
		panic(errDatabaseClosed)
	}

	if !key.IsAbsolute() {
		panic(ErrInvalidPathKey)
	}

	var (
		valueFound = core.True
		tx         = ctx.GetTx()
	)

	if tx == nil {
		err := kv.db.View(func(txn *Tx) error {
			_, err := txn.Get(string(key))
			if err == errNotFound {
				valueFound = core.False
				return nil
			}
			return err
		})

		if err != nil {
			panic(err)
		}

	} else {
		dbtx := kv.getCreateDatabaseTxn(db, tx)

		_, err := dbtx.Get(string(key))
		if err == errNotFound {
			valueFound = core.False
		} else if err != nil {
			panic(err)
		}

	}

	return valueFound
}

func (kv *KVStore) set(ctx *Context, key Path, value Value, db any) {

	if kv.db.isClosed() {
		panic(errDatabaseClosed)
	}

	if !key.IsAbsolute() {
		panic(ErrInvalidPathKey)
	}

	tx := ctx.GetTx()
	r := getFullResourceName(kv.host, key)

	if tx == nil {
		err := kv.db.Update(func(txn *Tx) error {
			repr := core.GetRepresentation(value, ctx)
			_, _, err := txn.Set(string(key), string(repr), nil)
			return err
		})

		if err != nil {
			panic(err)
		}

	} else {
		dbtx := kv.getCreateDatabaseTxn(db, tx)

		repr := core.GetRepresentation(value, ctx)
		if _, _, err := dbtx.Set(string(key), string(repr), nil); err != nil {
			panic(err)
		}

	}

	if err := ctx.AcquireResource(r); err != nil {
		panic(err)
	}
}

func makeTxEndcallbackFn(dbtx *Tx, tx *core.Transaction, kv *KVStore) func(t *core.Transaction, success bool) {
	return func(t *core.Transaction, success bool) {
		kv.transactionMapLock.Lock()
		delete(kv.transactions, tx)
		kv.transactionMapLock.Unlock()

		if success {
			if err := dbtx.Commit(); err != nil {
				panic(err)
			}
		} else if err := dbtx.Rollback(); err != nil {
			panic(err)
		}
	}
}
