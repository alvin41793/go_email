package db

import (
	"fmt"
	"time"
	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/mysql"
	"github.com/spf13/viper"
)

var db *gorm.DB

type Model struct {
	ID        uint       `gorm:"primary_key" json:"id"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `sql:"index" json:"deleted_at"`
}

//数据库对象
func DB() *gorm.DB {
	if db == nil {
		newDb, err := newDB()
		if err != nil {
			panic(err)
		}
		newDb.DB().SetMaxIdleConns(100)
		//newDb.DB().SetConnMaxLifetime(30000)
		newDb.DB().SetConnMaxLifetime(2 * time.Second)
		newDb.DB().SetMaxOpenConns(200)
		newDb.SingularTable(true)
		//newDb.LogMode(true)
		newDb.LogMode(false)
		db = newDb
	}
	return db
}

func newDB() (*gorm.DB, error) {
	config := fmt.Sprintf("%s:%s@tcp(%s)/%s?charset=utf8mb4&parseTime=%t&loc=%s",
		viper.GetString("db.username"),
		viper.GetString("db.password"),
		viper.GetString("db.addr"),
		viper.GetString("db.name"),
		true,
		//"Asia/Shanghai"),
		"Local")
	//fmt.Println(config)
	db, err := gorm.Open("mysql", config)
	if err != nil {
		return nil, err
	}
	// set for db connection
	return db, nil
}
