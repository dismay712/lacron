package lacron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule accepts numeric five- or six-field cron expressions. Five fields
// imply second 0. Weekdays are 0–6 (Sunday–Saturday).
// A field selecting its entire domain is treated as a wildcard. Restricted day-of-month
// and day-of-week fields use OR, matching BusyBox crond's FixDayDow behavior.
type Schedule struct {
	fields  [6][]bool
	full    [6]bool
	seconds bool
}

var bounds = [6][2]int{{0, 59}, {0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}

func ParseSchedule(expression string) (Schedule, error) {
	var s Schedule
	parts := strings.Fields(expression)
	if (len(parts) != 5 && len(parts) != 6) || len(expression) > 256 {
		return s, fmt.Errorf("cron 必须为 6 个字段：秒 分 时 日 月 星期；也兼容 5 字段（省略秒，固定为 0）")
	}
	s.seconds = len(parts) == 6
	if !s.seconds {
		parts = append([]string{"0"}, parts...)
	}
	for i, field := range parts {
		fieldName := [...]string{"秒", "分", "时", "日", "月", "星期"}[i]
		lo, hi := bounds[i][0], bounds[i][1]
		s.fields[i] = make([]bool, hi+1)
		for _, item := range strings.Split(field, ",") {
			pair := strings.Split(item, "/")
			if len(pair) > 2 || pair[0] == "" {
				return s, fmt.Errorf("%s字段格式无效", fieldName)
			}
			step := 1
			var err error
			if len(pair) == 2 {
				step, err = cronNumber(pair[1])
				if err != nil || step < 1 || step > hi-lo+1 {
					return s, fmt.Errorf("%s字段步长无效", fieldName)
				}
			}
			a, b := lo, hi
			if pair[0] != "*" {
				rangeParts := strings.Split(pair[0], "-")
				if len(rangeParts) > 2 || (len(pair) == 2 && len(rangeParts) != 2) {
					return s, fmt.Errorf("%s字段范围无效", fieldName)
				}
				a, err = cronNumber(rangeParts[0])
				if err != nil {
					return s, fmt.Errorf("%s字段仅支持数字、*、逗号、范围和步长", fieldName)
				}
				b = a
				if len(rangeParts) == 2 {
					b, err = cronNumber(rangeParts[1])
				}
				if err != nil || a < lo || b > hi || a > b {
					return s, fmt.Errorf("%s字段须在 %d–%d 范围内", fieldName, lo, hi)
				}
			}
			for value := a; value <= b; value += step {
				s.fields[i][value] = true
			}
		}
		s.full[i] = true
		for value := lo; value <= hi; value++ {
			s.full[i] = s.full[i] && s.fields[i][value]
		}
	}
	return s, nil
}

func cronNumber(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty number")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid number")
		}
	}
	return strconv.Atoi(s)
}

// Canonical expands steps and preserves the input's field count.
func (s Schedule) Canonical() string {
	parts := make([]string, 6)
	for i := range parts {
		if s.full[i] {
			parts[i] = "*"
			continue
		}
		values := []string{}
		for value := bounds[i][0]; value <= bounds[i][1]; value++ {
			if s.fields[i][value] {
				values = append(values, strconv.Itoa(value))
			}
		}
		parts[i] = strings.Join(values, ",")
	}
	if !s.seconds {
		parts = parts[1:]
	}
	return strings.Join(parts, " ")
}

func (s Schedule) matchesDay(t time.Time) bool {
	dom, dow := s.fields[3][t.Day()], s.fields[5][int(t.Weekday())]
	if s.full[3] {
		return dow
	}
	if s.full[5] {
		return dom
	}
	return dom || dow
}

func (s Schedule) Matches(t time.Time) bool {
	return s.fields[0][t.Second()] && s.fields[1][t.Minute()] &&
		s.fields[2][t.Hour()] && s.fields[4][int(t.Month())] && s.matchesDay(t)
}

func (s Schedule) Next(after time.Time) time.Time {
	t := after.Truncate(time.Second).Add(time.Second)
	limit := after.AddDate(8, 0, 0)
	for t.Before(limit) {
		if !s.fields[4][int(t.Month())] || !s.matchesDay(t) {
			next := time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			if !next.After(t) { // Some zones have a clock change at midnight.
				next = t.Add(time.Hour)
			}
			t = next
			continue
		}
		if !s.fields[2][t.Hour()] {
			t = t.Add(time.Duration(60-t.Minute())*time.Minute - time.Duration(t.Second())*time.Second)
			continue
		}
		if !s.fields[1][t.Minute()] {
			t = t.Add(time.Duration(60-t.Second()) * time.Second)
			continue
		}
		if s.fields[0][t.Second()] {
			return t
		}
		t = t.Add(time.Second)
	}
	return time.Time{}
}
