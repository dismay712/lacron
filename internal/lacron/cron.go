package lacron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a numeric, five-field cron expression. Weekdays are 0–6 (Sunday–Saturday).
// A field selecting its entire domain is treated as a wildcard. Restricted day-of-month
// and day-of-week fields use OR, matching BusyBox crond's FixDayDow behavior.
type Schedule struct {
	fields [5][]bool
	full   [5]bool
}

var bounds = [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}

func ParseSchedule(expression string) (Schedule, error) {
	var s Schedule
	parts := strings.Fields(expression)
	if len(parts) != 5 || len(expression) > 256 {
		return s, fmt.Errorf("cron 必须包含 5 个字段：分 时 日 月 星期")
	}
	for i, field := range parts {
		lo, hi := bounds[i][0], bounds[i][1]
		s.fields[i] = make([]bool, hi+1)
		for _, item := range strings.Split(field, ",") {
			pair := strings.Split(item, "/")
			if len(pair) > 2 || pair[0] == "" {
				return s, fmt.Errorf("第 %d 个字段格式无效", i+1)
			}
			step := 1
			var err error
			if len(pair) == 2 {
				step, err = cronNumber(pair[1])
				if err != nil || step < 1 || step > hi-lo+1 {
					return s, fmt.Errorf("第 %d 个字段步长无效", i+1)
				}
			}
			a, b := lo, hi
			if pair[0] != "*" {
				rangeParts := strings.Split(pair[0], "-")
				if len(rangeParts) > 2 || (len(pair) == 2 && len(rangeParts) != 2) {
					return s, fmt.Errorf("第 %d 个字段范围无效", i+1)
				}
				a, err = cronNumber(rangeParts[0])
				if err != nil {
					return s, fmt.Errorf("第 %d 个字段仅支持数字、*、逗号、范围和步长", i+1)
				}
				b = a
				if len(rangeParts) == 2 {
					b, err = cronNumber(rangeParts[1])
				}
				if err != nil || a < lo || b > hi || a > b {
					return s, fmt.Errorf("第 %d 个字段须在 %d–%d 范围内", i+1, lo, hi)
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

// Canonical expands steps before handing them to BusyBox. In particular, */2 in
// the day field starts at 1 here, whereas BusyBox's raw parser starts at 0.
func (s Schedule) Canonical() string {
	parts := make([]string, 5)
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
	return strings.Join(parts, " ")
}

func (s Schedule) matchesDay(t time.Time) bool {
	dom, dow := s.fields[2][t.Day()], s.fields[4][int(t.Weekday())]
	if s.full[2] {
		return dow
	}
	if s.full[4] {
		return dom
	}
	return dom || dow
}

func (s Schedule) Next(after time.Time) time.Time {
	t := after.Truncate(time.Minute).Add(time.Minute)
	limit := after.AddDate(8, 0, 0)
	for t.Before(limit) {
		if !s.fields[3][int(t.Month())] || !s.matchesDay(t) {
			next := time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			if !next.After(t) { // Some zones have a clock change at midnight.
				next = t.Add(time.Hour)
			}
			t = next
			continue
		}
		if !s.fields[1][t.Hour()] {
			t = t.Add(time.Duration(60-t.Minute()) * time.Minute)
			continue
		}
		if s.fields[0][t.Minute()] {
			return t
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}
}
