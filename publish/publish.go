package publish

import (
	"strings"

	"github.com/jinzhu/gorm"
	"github.com/qor/qor"

	"reflect"
)

const (
	PUBLISHED = false
	DIRTY     = true
)

type Status struct {
	PublishStatus bool
}

type Publish struct {
	DB              *gorm.DB
	SupportedModels []interface{}
}

func modelType(value interface{}) reflect.Type {
	reflectValue := reflect.Indirect(reflect.ValueOf(value))

	if reflectValue.Kind() == reflect.Slice {
		typ := reflectValue.Type().Elem()
		if typ.Kind() == reflect.Ptr {
			typ = typ.Elem()
		}
		return typ
	}

	return reflectValue.Type()
}

func New(db *gorm.DB) *Publish {
	db.Callback().Create().Before("gorm:begin_transaction").Register("publish:set_table_to_draft", SetTableAndPublishStatus(true))
	db.Callback().Create().Before("gorm:commit_or_rollback_transaction").
		Register("publish:sync_to_production_after_create", SyncToProductionAfterCreate)

	db.Callback().Delete().Before("gorm:begin_transaction").Register("publish:set_table_to_draft", SetTableAndPublishStatus(true))
	db.Callback().Delete().Replace("gorm:delete", Delete)
	db.Callback().Delete().Before("gorm:commit_or_rollback_transaction").
		Register("publish:sync_to_production_after_delete", SyncToProductionAfterDelete)

	db.Callback().Update().Before("gorm:begin_transaction").Register("publish:set_table_to_draft", SetTableAndPublishStatus(true))
	db.Callback().Update().Before("gorm:commit_or_rollback_transaction").
		Register("publish:sync_to_production", SyncToProductionAfterUpdate)

	db.Callback().RowQuery().Register("publish:set_table_in_draft_mode", SetTableAndPublishStatus(false))
	db.Callback().Query().Before("gorm:query").Register("publish:set_table_in_draft_mode", SetTableAndPublishStatus(false))
	return &Publish{DB: db}
}

func DraftTableName(table string) string {
	return OriginalTableName(table) + "_draft"
}

func OriginalTableName(table string) string {
	return strings.TrimSuffix(table, "_draft")
}

func (publish *Publish) Support(models ...interface{}) *Publish {
	for _, model := range models {
		scope := gorm.Scope{Value: model}
		for _, column := range []string{"DeletedAt", "PublishStatus"} {
			if !scope.HasColumn(column) {
				qor.ExitWithMsg("%v has no %v column", model, column)
			}
		}
		tableName := scope.TableName()
		publish.DB.SetTableNameHandler(model, func(db *gorm.DB) string {
			if db != nil {
				var forceDraftMode = false
				if forceMode, ok := db.Get("qor_publish:force_draft_mode"); ok {
					if forceMode, ok := forceMode.(bool); ok && forceMode {
						forceDraftMode = true
					}
				}

				if draftMode, ok := db.Get("qor_publish:draft_mode"); ok {
					if isDraft, ok := draftMode.(bool); ok && isDraft || forceDraftMode {
						return DraftTableName(tableName)
					}
				}
			}
			return tableName
		})
	}

	publish.SupportedModels = append(publish.SupportedModels, models...)

	var supportedModels []string
	for _, model := range publish.SupportedModels {
		supportedModels = append(supportedModels, modelType(model).String())
	}
	publish.DB.InstantSet("publish:support_models", supportedModels)
	return publish
}

func (db *Publish) AutoMigrate() {
	for _, value := range db.SupportedModels {
		db.DraftDB().AutoMigrate(value)
	}
}

func (db *Publish) ProductionDB() *gorm.DB {
	return db.DB.Set("qor_publish:draft_mode", false)
}

func (db *Publish) DraftDB() *gorm.DB {
	return db.DB.Set("qor_publish:draft_mode", true)
}

func (db *Publish) NewResolver(records ...interface{}) *Resolver {
	return &Resolver{Records: records, DB: db, Dependencies: map[string]*Dependency{}}
}

func (db *Publish) Publish(records ...interface{}) {
	db.NewResolver(records...).Publish()
}

func (db *Publish) Discard(records ...interface{}) {
	db.NewResolver(records...).Discard()
}
