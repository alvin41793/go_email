package db

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

var db *gorm.DB

type Model struct {
	ID        uint       `gorm:"primarykey" json:"id"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `gorm:"index" json:"deleted_at"`
}

// 数据库对象
func DB() *gorm.DB {
	if db == nil {
		newDb, err := newDB()
		if err != nil {
			panic(err)
		}

		sqlDB, err := newDb.DB()
		if err != nil {
			panic(err)
		}

		sqlDB.SetMaxIdleConns(100)
		sqlDB.SetConnMaxLifetime(30 * time.Minute) // 修复：从2秒改为30分钟
		sqlDB.SetMaxOpenConns(200)

		db = newDb
	}
	return db
}

func newDB() (*gorm.DB, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s)/%s?charset=utf8mb4&parseTime=%t&loc=%s",
		viper.GetString("db.username"),
		viper.GetString("db.password"),
		viper.GetString("db.addr"),
		viper.GetString("db.name"),
		true,
		"Local")

	config := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{
			SingularTable: true,
		},
	}

	return gorm.Open(mysql.Open(dsn), config)
}

// IsRecordNotFoundError 判断错误是否为记录未找到的错误
func IsRecordNotFoundError(err error) bool {
	return err == gorm.ErrRecordNotFound
}
