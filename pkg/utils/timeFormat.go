package utils

import (
	"database/sql/driver"
	"fmt"
	"time"
)

// 从time.go导入，删除重复的常量声明
// const Format = "2006-01-02 15:04:05"

type JsonTime struct {
	time.Time
}

func (t JsonTime) MarshalJSON() ([]byte, error) {

	formatTime := fmt.Sprintf("\"%s\"", t.Format(Format))
	if t.IsZero() {
		formatTime = "0"
	}
	return []byte(formatTime), nil
}

func (t *JsonTime) UnmarshalJSON(data []byte) (err error) {
	now, err := time.ParseInLocation(`"`+Format+`"`, string(data), time.Local)
	*t = JsonTime{now}
	if err != nil {
		return err
	}
	return
}

// Value insert timestamp into mysql need this function.
func (t JsonTime) Value() (driver.Value, error) {
	var zeroTime time.Time
	if t.Time.UnixNano() == zeroTime.UnixNano() {
		return nil, nil
	}
	return t.Time, nil
}

// Scan value of time.Time
func (t *JsonTime) Scan(v interface{}) error {
	value, ok := v.(time.Time)
	if ok {
		*t = JsonTime{Time: value}
		return nil
	}
	return fmt.Errorf("can not convert %v to timestamp", v)
}

func (t JsonTime) WeekTime() string {
	weekDay := t.Weekday().String()
	var weekMap = map[string]string{
		"Sunday":    "周日",
		"Monday":    "周一",
		"Tuesday":   "周二",
		"Wednesday": "周三",
		"Thursday":  "周四",
		"Friday":    "周五",
		"Saturday":  "周六",
	}
	return t.Format("2006-01-02") + " " + weekMap[weekDay] + " " + t.Format("15:04")
}
