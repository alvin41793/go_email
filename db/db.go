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

		// 优化数据库连接池配置
		configureConnectionPool(sqlDB)

		db = newDb
	}
	return db
}

// configureConnectionPool 配置数据库连接池
func configureConnectionPool(sqlDB interface {
	SetMaxIdleConns(n int)
	SetMaxOpenConns(n int)
	SetConnMaxLifetime(d time.Duration)
	SetConnMaxIdleTime(d time.Duration)
}) {
	// 根据配置文件读取连接池参数，如果没有配置则使用默认值
	maxIdleConns := viper.GetInt("db.max_idle_conns")
	if maxIdleConns <= 0 {
		maxIdleConns = 50 // 降低空闲连接数，避免过多连接占用资源
	}

	maxOpenConns := viper.GetInt("db.max_open_conns")
	if maxOpenConns <= 0 {
		maxOpenConns = 100 // 降低最大连接数，适应协程数量限制
	}

	connMaxLifetime := viper.GetDuration("db.conn_max_lifetime")
	if connMaxLifetime <= 0 {
		connMaxLifetime = 30 * time.Minute // 连接最大生命周期
	}

	connMaxIdleTime := viper.GetDuration("db.conn_max_idle_time")
	if connMaxIdleTime <= 0 {
		connMaxIdleTime = 10 * time.Minute // 连接最大空闲时间
	}

	// 设置连接池参数
	sqlDB.SetMaxIdleConns(maxIdleConns)
	sqlDB.SetMaxOpenConns(maxOpenConns)
	sqlDB.SetConnMaxLifetime(connMaxLifetime)
	sqlDB.SetConnMaxIdleTime(connMaxIdleTime)

	fmt.Printf("[数据库] 连接池配置: MaxIdle=%d, MaxOpen=%d, MaxLifetime=%v, MaxIdleTime=%v\n",
		maxIdleConns, maxOpenConns, connMaxLifetime, connMaxIdleTime)
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
