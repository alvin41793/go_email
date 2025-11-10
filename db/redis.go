package db

import (
	"fmt"
	"time"

	"github.com/go-redis/redis"
	"github.com/spf13/viper"
)

var globalClient *redis.Client = nil

func NewRedisDb() (*redis.Client, error) {
	addr := viper.GetString("redis.host")
	password := viper.GetString("redis.password")

	if globalClient != nil {
		_, err := globalClient.Ping().Result()
		if err != nil {
			globalClient.Close()
			globalClient = redis.NewClient(&redis.Options{
				Addr:     addr,
				Password: password,
				DB:       0,
			})
		}
	} else {
		globalClient = redis.NewClient(&redis.Options{
			Addr:     addr,
			Password: password,
			DB:       0,
		})
	}

	// use different db
	if viper.GetString("runmode") == "debug" {
		globalClient.Do("SELECT", 2)
	}

	//fmt.Println(redis.Options{Addr: addr,
	//	password: REDACTED

	// 通过 cient.Ping() 来检查是否成功连接到了 redis 服务器
	/*pong, err := globalClient.Ping().Result()
	if err != nil {
		fmt.Println(pong, err)
		return nil, err
	}*/
	//fmt.Println("redis connect success")
	return globalClient, nil
}

// 连接池
func NewRedisPoolDb() (*redis.Client, error) {
	addr := viper.GetString("redis.host")
	password := viper.GetString("redis.password")
	client := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DialTimeout:  10 * time.Second,
		ReadTimeout:  20 * time.Second,
		WriteTimeout: 20 * time.Second,
		PoolSize:     100,
		PoolTimeout:  20 * time.Second,
	})

	// use different db
	if viper.GetString("runmode") == "debug" {
		client.Do("SELECT", 2)
	}
	pong, err := client.Ping().Result()
	if err != nil {
		fmt.Println("pong redis pool"+pong, err)
		return nil, err
	}
	return client, nil
}
