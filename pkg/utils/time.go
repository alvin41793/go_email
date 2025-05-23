package utils

import "time"

const Format = "2006-01-02 15:04:05"

func Now() string {
	return time.Now().Format(Format)
}

//TenMinutesLater 十分钟之后
func TenMinutesLater() time.Time {
	return time.Now().Add(+time.Minute * 10)
}

func TimeWeekTime(t time.Time) string {
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
