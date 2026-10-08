package models

import (
	"encoding/json"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type JSON json.RawMessage

type JSONMap map[string]any

func (j JSON) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return json.RawMessage(nil).MarshalJSON()
	}
	return json.RawMessage(j).MarshalJSON()
}

func (j *JSON) UnmarshalJSON(data []byte) error {
	return (*json.RawMessage)(j).UnmarshalJSON(data)
}

func (JSON) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	switch db.Dialector.Name() {
	case "postgres":
		return "JSONB"
	case "sqlite":
		return "JSON"
	default:
		panic(fmt.Sprintf("unsupported JSON dialect: %s", db.Dialector.Name()))
	}
}

func (JSONMap) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	switch db.Dialector.Name() {
	case "postgres":
		return "JSONB"
	case "sqlite":
		return "JSON"
	default:
		panic(fmt.Sprintf("unsupported JSON dialect: %s", db.Dialector.Name()))
	}
}
