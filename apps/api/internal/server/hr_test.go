package server_test

import (
	"encoding/csv"
	"strings"
	"testing"
	"time"
)

// Attendance, leave and holidays (WP10). Days are IST; fixtures come from the Rust server.

// emp finds a member's employee id.
func (b *bt) emp(userID string) string {
	b.t.Helper()
	for _, r := range b.okList("employee.list", map[string]any{}) {
		m := r.(map[string]any)
		if m["user_id"] == userID {
			return m["id"].(string)
		}
	}
	b.t.Fatalf("no employee for %s", userID)
	return ""
}

// open makes today a working day for everyone (no weekly off, no holiday), so counts do not depend on the weekday.
func (b *bt) open() {
	b.t.Helper()
	for _, r := range b.okList("employee.list", map[string]any{}) {
		b.ok("employee.update", map[string]any{"id": r.(map[string]any)["id"], "weekly_off": []int{}})
	}
	for _, h := range b.okList("holiday.list", map[string]any{}) {
		if m := h.(map[string]any); m["day"] == day(0) || m["day"] == day(-1) {
			b.ok("holiday.delete", map[string]any{"id": m["id"]})
		}
	}
}

// monday returns a Monday at least a week ahead whose Monday-to-Sunday week sits in one calendar year.
func monday() time.Time {
	d := todayIST().AddDate(0, 0, 7)
	for d.Weekday() != time.Monday || d.Year() != d.AddDate(0, 0, 6).Year() {
		d = d.AddDate(0, 0, 1)
	}
	return d
}

func ds(t time.Time) string { return t.Format("2006-01-02") }

func (b *bt) leaveType(name string) string {
	b.t.Helper()
	for _, r := range b.okList("leave_type.list", map[string]any{}) {
		if m := r.(map[string]any); m["name"] == name {
			return m["id"].(string)
		}
	}
	b.t.Fatalf("no leave type %s", name)
	return ""
}

func (b *bt) count(sql string) string {
	b.t.Helper()
	return b.env.dbStrings(b.owner.ws, sql)[0]
}

func TestAttendanceCheckInOutOneRowPerDay(t *testing.T) {
	b := newBT(t, false)
	m := b.memberAs("Mina", "member")
	in := b.okAs(m, "attendance.check_in", map[string]any{})
	eq(t, in["status"], "present")
	eq(t, in["day"], day(0)) // the IST day
	first := in["in_at"]
	again := b.okAs(m, "attendance.check_in", map[string]any{}) // a second tap keeps the first time
	eq(t, again["in_at"], first)
	eq(t, b.count("select count(*)::text from hr.attendance a join hr.employees e on e.id = a.employee_id where e.user_id = '"+m.userID+"'"), "1")
	eq(t, b.count("select count(*)::text from core.events where kind = 'attendance.marked'"), "1")

	out := b.okAs(m, "attendance.check_out", map[string]any{})
	if out["out_at"] == nil {
		t.Fatalf("check_out left no time: %v", out)
	}
	// no check-in, no check-out
	o := b.memberAs("Omar", "member")
	b.errAs(o, "attendance.check_out", map[string]any{}, 409, "conflict")
	// presence is a person's own act: keys and guests are refused
	b.errAs(b.env.apiKey(b.owner), "attendance.check_in", map[string]any{}, 403, "forbidden")
	b.errAs(b.memberAs("Gus", "guest"), "attendance.check_in", map[string]any{}, 403, "forbidden")
	// another workspace has its own people
	o2 := b.env.signup("Other", true)
	b.env.cmd(b.gosv.URL, o2, o2.ws, "attendance.check_in", map[string]any{}, nil)
	if l := b.env.cmd(b.gosv.URL, o2, o2.ws, "attendance.today", map[string]any{}, nil); strings.Contains(string(l.body), "Mina") || !strings.Contains(string(l.body), `"present":1`) {
		t.Fatalf("other workspace sees %s", l.body)
	}
}

func TestAttendanceMarkBackdatingAndRoles(t *testing.T) {
	b := newBT(t, false)
	m := b.memberAs("Mina", "member")
	a := b.memberAs("Ana", "admin")
	b.okAs(m, "attendance.check_in", map[string]any{}) // creates employee records
	emp := b.emp(m.userID)
	d := day(-1)
	got := b.okAs(a, "attendance.mark", map[string]any{"employee_id": emp, "day": d, "status": "absent", "note": "no call"})
	eq(t, got["status"], "absent")
	got = b.okAs(a, "attendance.mark", map[string]any{"employee_id": emp, "day": d, "status": "half_day"}) // upsert, not a second row
	eq(t, got["status"], "half_day")
	eq(t, b.count("select count(*)::text from hr.attendance where employee_id = '"+emp+"' and day = '"+d+"'"), "1")

	for name, in := range map[string]map[string]any{
		"two months back": {"employee_id": emp, "day": ds(todayIST().AddDate(0, -2, 0)), "status": "present"},
		"future":          {"employee_id": emp, "day": day(1), "status": "present"},
		"bad status":      {"employee_id": emp, "day": d, "status": "leave"},
		"bad day":         {"employee_id": emp, "day": "01-04-2026", "status": "present"},
		"bad employee id": {"employee_id": "x", "day": d, "status": "present"},
	} {
		if r := b.call(a, "attendance.mark", in); r.status != 400 {
			t.Errorf("%s: want 400, got %d %s", name, r.status, r.body)
		}
	}
	b.errAs(a, "attendance.mark", map[string]any{"employee_id": "00000000-0000-4000-8000-000000000000", "day": d, "status": "absent"}, 404, "not_found")
	b.errAs(m, "attendance.mark", map[string]any{"employee_id": emp, "day": d, "status": "absent"}, 403, "forbidden")
	b.errAs(b.env.apiKey(b.owner), "attendance.mark", map[string]any{"employee_id": emp, "day": d, "status": "absent"}, 403, "forbidden")
}

func TestTodayBoardScopeCountsAndHomeTile(t *testing.T) {
	b := newBT(t, false)
	mina, nik, zed := b.memberAs("Mina", "member"), b.memberAs("Nik", "member"), b.memberAs("Zed", "member")
	b.ok("member.update", map[string]any{"user_id": nik.userID, "manager_user_id": mina.userID})
	b.okAs(nik, "attendance.check_in", map[string]any{})
	ravi := b.ok("employee.create", map[string]any{"name": "Ravi Kumar", "code": "E-7"}) // staff without a login
	b.errAs(b.memberAs("Ana", "admin"), "employee.create", map[string]any{"name": "Dup", "code": "E-7"}, 409, "conflict")
	b.open()
	b.ok("attendance.mark", map[string]any{"employee_id": ravi["id"], "day": day(0), "status": "present"})
	b.ok("attendance.mark", map[string]any{"employee_id": b.emp(zed.userID), "day": day(0), "status": "absent"})

	today := b.ok("attendance.today", map[string]any{})
	c := today["counts"].(map[string]any)
	eq(t, c["expected"], float64(6)) // owner, Mina, Nik, Zed, Ana, Ravi
	eq(t, c["present"], float64(2))
	eq(t, c["absent"], float64(1))
	eq(t, c["not_marked"], float64(3))
	h := b.home(b.owner)
	eq(t, tile(t, h, "people_present")["present"], float64(2))
	eq(t, tile(t, h, "people_present")["expected"], float64(6))

	// a manager sees herself and her reports; a plain member only herself; neither gets the owner tile
	mc := b.okAs(mina, "attendance.today", map[string]any{})["counts"].(map[string]any)
	eq(t, mc["expected"], float64(2))
	eq(t, mc["present"], float64(1))
	zc := b.okAs(zed, "attendance.today", map[string]any{})
	eq(t, zc["counts"].(map[string]any)["expected"], float64(1))
	eq(t, zc["me"].(map[string]any)["status"], "absent")
	noTile(t, b.home(mina), "people_present")
	b.errAs(b.memberAs("Gus", "guest"), "attendance.today", map[string]any{}, 403, "forbidden")

	// someone who left no longer counts
	b.ok("member.deactivate", map[string]any{"user_id": zed.userID})
	eq(t, b.ok("attendance.today", map[string]any{})["counts"].(map[string]any)["expected"], float64(5))
	// a weekly off is not expected: Ravi off today
	b.ok("employee.update", map[string]any{"id": ravi["id"], "weekly_off": []int{int(todayIST().Weekday())}})
	eq(t, b.ok("attendance.today", map[string]any{})["counts"].(map[string]any)["expected"], float64(5)) // present on his off day still counts
	b.ok("attendance.mark", map[string]any{"employee_id": ravi["id"], "day": day(0), "status": "week_off"})
	eq(t, b.ok("attendance.today", map[string]any{})["counts"].(map[string]any)["expected"], float64(4))
}

func TestLeaveRequestDaysBalanceOverlapAndVisibility(t *testing.T) {
	b := newBT(t, false)
	mina, nik, zed := b.memberAs("Mina", "member"), b.memberAs("Nik", "member"), b.memberAs("Zed", "member")
	b.ok("member.update", map[string]any{"user_id": nik.userID, "manager_user_id": mina.userID})
	casual := b.leaveType("Casual leave")
	eq(t, b.okList("holiday.list", map[string]any{"year": 2027})[0].(map[string]any)["name"], "Republic Day")
	mon := monday()
	b.ok("holiday.set", map[string]any{"day": ds(mon.AddDate(0, 0, 2)), "name": "Founders day", "kind": "company"})

	// Monday to Sunday: the Sunday weekly off and the Wednesday holiday are not leave days
	req := b.okAs(nik, "leave.request", map[string]any{"leave_type_id": casual, "from_day": ds(mon), "to_day": ds(mon.AddDate(0, 0, 6)), "reason": "wedding"})
	eq(t, req["days"], float64(5))
	eq(t, req["status"], "pending")
	eq(t, req["can_decide"], false) // not for the person who asked
	id := req["id"].(string)

	// invalid or refused
	for name, in := range map[string]map[string]any{
		"overlap":        {"leave_type_id": casual, "from_day": ds(mon.AddDate(0, 0, 1)), "to_day": ds(mon.AddDate(0, 0, 1))},
		"to before from": {"leave_type_id": casual, "from_day": ds(mon.AddDate(0, 0, 8)), "to_day": ds(mon.AddDate(0, 0, 7))},
		"all days off":   {"leave_type_id": casual, "from_day": ds(mon.AddDate(0, 0, 13)), "to_day": ds(mon.AddDate(0, 0, 13))}, // a Sunday
		"half multi day": {"leave_type_id": casual, "from_day": ds(mon.AddDate(0, 0, 14)), "to_day": ds(mon.AddDate(0, 0, 15)), "half_day": true},
		"two years":      {"leave_type_id": casual, "from_day": "2026-12-30", "to_day": "2027-01-04"},
		"long ago":       {"leave_type_id": casual, "from_day": ds(todayIST().AddDate(0, -3, 0))},
		"unknown type":   {"leave_type_id": "00000000-0000-4000-8000-000000000000", "from_day": ds(mon.AddDate(0, 0, 14))},
		"bad date":       {"leave_type_id": casual, "from_day": "next thursday"},
	} {
		r := b.call(nik, "leave.request", in)
		want := 400
		if name == "overlap" {
			want = 409
		}
		if r.status != want {
			t.Errorf("%s: want %d, got %d %s", name, want, r.status, r.body)
		}
	}

	// the balance is never overdrawn unless the type has no limit
	tiny := b.ok("leave_type.set", map[string]any{"name": "Tiny", "yearly_quota": 1})["id"].(string)
	b.errAs(nik, "leave.request", map[string]any{"leave_type_id": tiny, "from_day": ds(mon.AddDate(0, 0, 14)), "to_day": ds(mon.AddDate(0, 0, 15))}, 409, "conflict")
	free := b.ok("leave_type.set", map[string]any{"name": "Unpaid", "yearly_quota": nil, "paid": false})["id"].(string)
	b.okAs(nik, "leave.request", map[string]any{"leave_type_id": free, "from_day": ds(mon.AddDate(0, 0, 14)), "to_day": ds(mon.AddDate(0, 0, 15))})
	half := b.okAs(nik, "leave.request", map[string]any{"leave_type_id": casual, "from_day": ds(mon.AddDate(0, 0, 21)), "half_day": true})
	eq(t, half["days"], 0.5)
	b.okAs(nik, "leave.cancel", map[string]any{"id": half["id"]})
	b.ok("leave_type.set", map[string]any{"id": tiny, "active": false})
	b.errAs(nik, "leave.request", map[string]any{"leave_type_id": tiny, "from_day": ds(mon.AddDate(0, 0, 21))}, 400, "invalid")

	bal := func(w who) map[string]any {
		for _, r := range b.list(w, "leave.balance", map[string]any{}) {
			if m := r.(map[string]any); m["name"] == "Casual leave" {
				return m
			}
		}
		return nil
	}
	eq(t, bal(nik)["pending"], float64(5))
	eq(t, bal(nik)["remaining"], float64(7))

	// who sees what: the employee, their manager and admins; not a colleague
	eq(t, len(b.list(nik, "leave.list", map[string]any{})), 3)
	eq(t, len(b.list(mina, "leave.list", map[string]any{})), 3)
	eq(t, len(b.list(b.owner, "leave.list", map[string]any{"status": "pending"})), 2)
	eq(t, len(b.list(zed, "leave.list", map[string]any{})), 0)
	b.errAs(zed, "leave.balance", map[string]any{"employee_id": b.emp(nik.userID)}, 404, "not_found")
	b.errAs(zed, "leave.decide", map[string]any{"id": id, "decision": "approve"}, 404, "not_found")
	b.errAs(zed, "leave.cancel", map[string]any{"id": id}, 404, "not_found")
	eq(t, len(b.list(mina, "leave.balance", map[string]any{"employee_id": b.emp(nik.userID)})) > 0, true)
	// keys and agents never request or decide
	key := b.env.apiKey(b.owner)
	b.errAs(key, "leave.request", map[string]any{"leave_type_id": casual, "from_day": ds(mon)}, 403, "forbidden")
	b.errAs(key, "leave.decide", map[string]any{"id": id, "decision": "approve"}, 403, "forbidden")

	// the manager decides; rejecting needs a reason; nobody decides their own
	b.errAs(nik, "leave.decide", map[string]any{"id": id, "decision": "approve"}, 403, "forbidden")
	b.errAs(mina, "leave.decide", map[string]any{"id": id, "decision": "reject"}, 400, "invalid")
	b.errAs(mina, "leave.decide", map[string]any{"id": id, "decision": "maybe"}, 400, "invalid")
	got := b.okAs(mina, "leave.decide", map[string]any{"id": id, "decision": "approve"})
	eq(t, got["status"], "approved")
	eq(t, got["decided_by"], "Mina")
	b.errAs(mina, "leave.decide", map[string]any{"id": id, "decision": "approve"}, 409, "conflict")
	// approved leave is on the attendance sheet: five days, never the Sunday or the holiday
	nikEmp := b.emp(nik.userID)
	eq(t, b.count("select count(*)::text from hr.attendance where employee_id = '"+nikEmp+"' and status = 'leave'"), "5")
	eq(t, b.count("select count(*)::text from hr.attendance where employee_id = '"+nikEmp+"' and day = '"+ds(mon.AddDate(0, 0, 2))+"'"), "0")
	eq(t, bal(nik)["used"], float64(5))
	// cancelling approved leave takes its days off the sheet again
	b.okAs(nik, "leave.cancel", map[string]any{"id": id})
	eq(t, b.count("select count(*)::text from hr.attendance where employee_id = '"+nikEmp+"' and status = 'leave'"), "0")
	b.errAs(nik, "leave.cancel", map[string]any{"id": id}, 409, "conflict")
	eq(t, bal(nik)["used"], float64(0))

	// reject with a reason; the days are free again
	r2 := b.okAs(nik, "leave.request", map[string]any{"leave_type_id": casual, "from_day": ds(mon), "to_day": ds(mon.AddDate(0, 0, 1))})
	rej := b.okAs(b.owner, "leave.decide", map[string]any{"id": r2["id"], "decision": "reject", "reason": "peak week"})
	eq(t, rej["status"], "rejected")
	eq(t, rej["decision_note"], "peak week")
	eq(t, b.count("select count(*)::text from hr.attendance where employee_id = '"+nikEmp+"' and status = 'leave'"), "0")

	// the owner decides their own leave; an admin cannot
	ana := b.memberAs("Ana", "admin")
	own := b.okAs(ana, "leave.request", map[string]any{"leave_type_id": casual, "from_day": ds(mon.AddDate(0, 0, 3))})
	b.errAs(ana, "leave.decide", map[string]any{"id": own["id"], "decision": "approve"}, 403, "forbidden")
	b.okAs(b.owner, "leave.decide", map[string]any{"id": own["id"], "decision": "approve"})
	for _, k := range []string{"leave.requested", "leave.approved", "leave.rejected", "leave.cancelled"} {
		if b.count("select count(*)::text from core.events where kind = '"+k+"'") == "0" {
			t.Errorf("no %s event", k)
		}
	}
}

func TestLeaveShowsOnTodayAndReplay(t *testing.T) {
	b := newBT(t, false)
	nik := b.memberAs("Nik", "member")
	b.okAs(nik, "attendance.check_in", map[string]any{})
	b.open()
	// today's check-in means leave cannot overwrite the real time; ask for tomorrow's board instead via SQL-free route:
	// a second person with leave today
	zed := b.memberAs("Zed", "member")
	b.ok("employee.update", map[string]any{"id": b.emp(zed.userID), "weekly_off": []int{}})
	casual := b.leaveType("Casual leave")
	h := map[string]string{"Idempotency-Key": "leave-" + rnd()}
	r1 := b.env.cmd(b.gosv.URL, zed, b.owner.ws, "leave.request", map[string]any{"leave_type_id": casual, "from_day": day(0)}, h)
	r2 := b.env.cmd(b.gosv.URL, zed, b.owner.ws, "leave.request", map[string]any{"leave_type_id": casual, "from_day": day(0)}, h)
	if r1.status != 200 || r2.status != 200 {
		t.Fatalf("replay: %d %s / %d %s", r1.status, r1.body, r2.status, r2.body)
	}
	eq(t, r1.json()["id"], r2.json()["id"])
	eq(t, b.count("select count(*)::text from hr.leave_requests"), "1")
	b.ok("leave.decide", map[string]any{"id": r1.json()["id"], "decision": "approve"})
	c := b.ok("attendance.today", map[string]any{})["counts"].(map[string]any)
	eq(t, c["on_leave"], float64(1))
	eq(t, c["present"], float64(1))
	eq(t, tile(t, b.home(b.owner), "people_present")["on_leave"], float64(1))
	// an admin cannot overwrite an approved leave day by marking it
	past := b.okAs(zed, "leave.request", map[string]any{"leave_type_id": casual, "from_day": day(-1)})
	b.ok("leave.decide", map[string]any{"id": past["id"], "decision": "approve"})
	b.errAs(b.owner, "attendance.mark", map[string]any{"employee_id": b.emp(zed.userID), "day": day(-1), "status": "absent"}, 409, "conflict")
	// checking in on a leave day turns it into a present day
	z := b.okAs(zed, "attendance.check_in", map[string]any{})
	eq(t, z["status"], "present")
}

func TestHolidaysLeaveTypesAndRoles(t *testing.T) {
	b := newBT(t, false)
	m := b.memberAs("Mina", "member")
	list := b.list(m, "holiday.list", map[string]any{"year": 2027})
	eq(t, len(list), 3)
	names := []string{}
	for _, h := range list {
		names = append(names, h.(map[string]any)["day"].(string))
	}
	eq(t, strings.Join(names, ","), "2027-01-26,2027-08-15,2027-10-02")
	got := b.ok("holiday.set", map[string]any{"day": "2027-11-05", "name": "Diwali", "kind": "festival"})
	b.ok("holiday.set", map[string]any{"day": "2027-11-05", "name": "Deepavali"}) // same day: renamed, still one row
	eq(t, len(b.list(m, "holiday.list", map[string]any{"year": 2027})), 4)
	b.errAs(b.owner, "holiday.set", map[string]any{"day": "2027-11-06", "name": "X", "kind": "national"}, 400, "invalid")
	b.errAs(m, "holiday.set", map[string]any{"day": "2027-11-06", "name": "X"}, 403, "forbidden")
	b.errAs(m, "holiday.delete", map[string]any{"id": got["id"]}, 403, "forbidden")
	b.ok("holiday.delete", map[string]any{"id": got["id"]})
	b.errAs(b.owner, "holiday.delete", map[string]any{"id": got["id"]}, 404, "not_found")
	// a deleted national holiday stays deleted (seeded once per year)
	for _, h := range b.list(m, "holiday.list", map[string]any{"year": 2027}) {
		if hm := h.(map[string]any); hm["day"] == "2027-08-15" {
			b.ok("holiday.delete", map[string]any{"id": hm["id"]})
		}
	}
	eq(t, len(b.list(m, "holiday.list", map[string]any{"year": 2027})), 2)

	types := b.list(m, "leave_type.list", map[string]any{})
	eq(t, len(types), 3)
	eq(t, types[0].(map[string]any)["name"], "Casual leave")
	eq(t, types[0].(map[string]any)["yearly_quota"], float64(12))
	b.errAs(m, "leave_type.set", map[string]any{"name": "Study"}, 403, "forbidden")
	b.errAs(b.owner, "leave_type.set", map[string]any{"name": "Casual leave"}, 409, "conflict")
	b.errAs(b.owner, "leave_type.set", map[string]any{"name": ""}, 400, "invalid")
	b.errAs(b.owner, "leave_type.set", map[string]any{"id": "00000000-0000-4000-8000-000000000000", "name": "Z"}, 404, "not_found")
	upd := b.ok("leave_type.set", map[string]any{"id": b.leaveType("Casual leave"), "yearly_quota": 10})
	eq(t, upd["yearly_quota"], float64(10))
	// employee admin: code, weekly off, activity
	ravi := b.ok("employee.create", map[string]any{"name": "Ravi", "weekly_off": []int{0, 6}})
	eq(t, len(ravi["weekly_off"].([]any)), 2)
	b.errAs(b.owner, "employee.create", map[string]any{"name": "Bad", "weekly_off": []int{7}}, 400, "invalid")
	b.errAs(b.owner, "employee.update", map[string]any{"id": b.emp(m.userID), "name": "Other"}, 400, "invalid") // a member's name is their account's
	b.errAs(m, "employee.list", map[string]any{}, 403, "forbidden")
	// another workspace has its own defaults and none of ours
	o2 := b.env.signup("Other", true)
	l2 := b.env.cmd(b.gosv.URL, o2, o2.ws, "holiday.list", map[string]any{"year": 2027}, nil)
	eq(t, strings.Count(string(l2.body), "Republic Day"), 1)
	eq(t, strings.Contains(string(l2.body), "Deepavali"), false)
}

func TestRegisterCSVConfirmationAndExport(t *testing.T) {
	b := newBT(t, false)
	nik := b.memberAs("Nik", "member")
	b.okAs(nik, "attendance.check_in", map[string]any{})
	b.ok("employee.create", map[string]any{"name": "=HYPERLINK(\"http://x\")", "code": "E-1"})
	first := ds(time.Date(todayIST().Year(), todayIST().Month(), 1, 0, 0, 0, 0, time.UTC))
	month := first[:7]
	b.ok("attendance.mark", map[string]any{"employee_id": b.emp(nik.userID), "day": first, "status": "present"})

	reg := b.ok("attendance.register", map[string]any{"month": month})
	rows, err := csv.NewReader(strings.NewReader(reg["csv"].(string))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	eq(t, rows[0][0], "Code")
	eq(t, rows[0][len(rows[0])-1], "Not marked")
	formula := false
	for _, r := range rows[1:] {
		if r[0] == "E-1" {
			formula = strings.HasPrefix(r[1], "'=") // never a live formula in a spreadsheet
		}
	}
	eq(t, formula, true)
	for _, r := range reg["rows"].([]any) {
		if m := r.(map[string]any); m["name"] == "Nik" {
			eq(t, m["totals"].(map[string]any)["present"].(float64) >= 1, true)
			eq(t, m["days"].([]any)[0], "P")
		}
	}
	eq(t, b.count("select count(*)::text from core.events where kind = 'attendance.register_read'"), "1")
	b.errAs(b.owner, "attendance.register", map[string]any{"month": "April"}, 400, "invalid")
	b.errAs(nik, "attendance.register", map[string]any{"month": month}, 403, "forbidden")
	// bulk personal data to a machine caller waits for a person
	b.enableGroups("read")
	agent := b.agentKey("payroll-bot")
	eq(t, b.okAs(agent, "attendance.register", map[string]any{"month": month})["confirmation_required"], true)

	// DPDP: the member's export carries their employee record, attendance and leave
	casual := b.leaveType("Casual leave")
	b.okAs(nik, "leave.request", map[string]any{"leave_type_id": casual, "from_day": ds(monday())})
	exp := b.ok("data.export_subject", map[string]any{"user_id": nik.userID})["data"].(map[string]any)
	eq(t, len(exp["employee"].([]any)), 1)
	eq(t, len(exp["attendance"].([]any)) >= 1, true)
	eq(t, len(exp["leave_requests"].([]any)), 1)
	// an employee of another workspace is never in it
	o2 := b.env.signup("Other", true)
	b.env.cmd(b.gosv.URL, o2, o2.ws, "attendance.check_in", map[string]any{}, nil)
	b.errAs(b.owner, "data.export_subject", map[string]any{"user_id": o2.userID}, 404, "not_found")
	r := b.env.cmd(b.gosv.URL, o2, o2.ws, "attendance.register", map[string]any{"month": month}, nil)
	eq(t, strings.Contains(string(r.body), "Nik"), false)
}
