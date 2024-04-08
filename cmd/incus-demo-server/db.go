package main

import (
	"database/sql"
	"fmt"
	"net"
	"time"

	"github.com/mattn/go-sqlite3"
)

// Global variables.
var db *sql.DB

func dbSetup() error {
	var err error

	db, err = sql.Open("sqlite3", fmt.Sprintf("database.sqlite3?_busy_timeout=5000&_txlock=exclusive"))
	if err != nil {
		return err
	}

	err = dbCreateTables()
	if err != nil {
		return err
	}

	return nil
}

func dbCreateTables() error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    uuid VARCHAR(36) NOT NULL,
    status INTEGER NOT NULL,
    instance_name VARCHAR(64) NOT NULL,
    instance_ip VARCHAR(39) NOT NULL,
    instance_username VARCHAR(10) NOT NULL,
    instance_password VARCHAR(10) NOT NULL,
    instance_expiry INT NOT NULL,
    request_date INT NOT NULL,
    request_ip VARCHAR(39) NOT NULL,
    request_terms VARCHAR(64) NOT NULL
);

CREATE TABLE IF NOT EXISTS feedback (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    session_id INTEGER NOT NULL,
    rating INTEGER,
    email VARCHAR(255),
    email_use INTEGER,
    feedback TEXT,
    FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE
);
`)
	if err != nil {
		return err
	}

	return nil
}

func dbGetStats(period string, unique bool, network *net.IPNet) (int64, error) {
	var count int64

	// Deal with unique filter.
	what := "request_ip"
	if unique {
		what = "distinct request_ip"
	}

	// Deal with period filter.
	where := ""
	if period == "current" {
		where = "WHERE status=0"
	} else if period == "hour" {
		creation := time.Now().Add(-time.Hour).Unix()
		where = fmt.Sprintf("WHERE request_date > %d", creation)
	} else if period == "day" {
		creation := time.Now().Add(-time.Hour * 24).Unix()
		where = fmt.Sprintf("WHERE request_date > %d", creation)
	} else if period == "week" {
		creation := time.Now().Add(-time.Hour * 24 * 7).Unix()
		where = fmt.Sprintf("WHERE request_date > %d", creation)
	} else if period == "month" {
		creation := time.Now().Add(-time.Hour * time.Duration(24*30.5)).Unix()
		where = fmt.Sprintf("WHERE request_date > %d", creation)
	} else if period == "year" {
		creation := time.Now().Add(-time.Hour * time.Duration(24*365.25)).Unix()
		where = fmt.Sprintf("WHERE request_date > %d", creation)
	}

	if network == nil {
		err := db.QueryRow(fmt.Sprintf("SELECT count(%s) FROM sessions %s;", what, where)).Scan(&count)
		if err != nil {
			return -1, err
		}
	} else {
		outfmt := []interface{}{""}

		q := fmt.Sprintf("SELECT %s FROM sessions %s;", what, where)
		result, err := dbQueryScan(db, q, nil, outfmt)
		if err != nil {
			return -1, err
		}

		for _, ip := range result {
			netIp := net.ParseIP(ip[0].(string))
			if netIp == nil {
				continue
			}

			if !network.Contains(netIp) {
				continue
			}

			count += 1
		}
	}

	return count, nil
}

func dbShouldExist(name string) (bool, error) {
	var count int64

	statement := `SELECT COUNT(id) FROM sessions WHERE instance_name=? AND status IN (0, 2);`
	err := db.QueryRow(statement, name).Scan(&count)
	if err != nil {
		return false, err
	}

	return count == 1, nil
}

func dbActive() ([][]interface{}, error) {
	q := fmt.Sprintf("SELECT id, instance_name, instance_expiry FROM sessions WHERE status=0;")
	var instanceID int
	var instanceName string
	var instanceExpiry int
	outfmt := []interface{}{instanceID, instanceName, instanceExpiry}
	result, err := dbQueryScan(db, q, nil, outfmt)
	if err != nil {
		return nil, err
	}

	return result, nil
}

func dbAllocated() ([][]interface{}, error) {
	q := fmt.Sprintf("SELECT id, instance_name, instance_expiry FROM sessions WHERE status=2;")
	var instanceID int
	var instanceName string
	var instanceExpiry int
	outfmt := []interface{}{instanceID, instanceName, instanceExpiry}
	result, err := dbQueryScan(db, q, nil, outfmt)
	if err != nil {
		return nil, err
	}

	return result, nil
}

func dbGetInstance(id string, active bool) (int64, string, string, string, string, int64, error) {
	var sessionId int64
	var instanceName string
	var instanceIP string
	var instanceUsername string
	var instancePassword string
	var instanceExpiry int64
	var err error
	var rows *sql.Rows

	sessionId = -1

	if active {
		rows, err = dbQuery(db, "SELECT id, instance_name, instance_ip, instance_username, instance_password, instance_expiry FROM sessions WHERE status=0 AND uuid=?;", id)
	} else {
		rows, err = dbQuery(db, "SELECT id, instance_name, instance_ip, instance_username, instance_password, instance_expiry FROM sessions WHERE uuid=?;", id)
	}
	if err != nil {
		return -1, "", "", "", "", 0, err
	}

	defer rows.Close()

	for rows.Next() {
		rows.Scan(&sessionId, &instanceName, &instanceIP, &instanceUsername, &instancePassword, &instanceExpiry)
	}

	return sessionId, instanceName, instanceIP, instanceUsername, instancePassword, instanceExpiry, nil
}

func dbGetFeedback(id int64) (int64, int64, string, int64, string, error) {
	var feedbackId int64
	var rating int64
	var email string
	var emailUse int64
	var feedback string

	feedbackId = -1
	rating = -1
	emailUse = -1
	rows, err := dbQuery(db, "SELECT id, rating, email, email_use, feedback FROM feedback WHERE session_id=?;", id)
	if err != nil {
		return -1, -1, "", -1, "", err
	}

	defer rows.Close()

	for rows.Next() {
		rows.Scan(&feedbackId, &rating, &email, &emailUse, &feedback)
	}

	return feedbackId, rating, email, emailUse, feedback, nil
}

func dbNew(status int, id string, instanceName string, instanceIP string, instanceUsername string, instancePassword string, instanceExpiry int64, requestDate int64, requestIP string, requestTerms string) (int64, error) {
	res, err := db.Exec(`
INSERT INTO sessions (
	status,
	uuid,
	instance_name,
	instance_ip,
	instance_username,
	instance_password,
	instance_expiry,
	request_date,
	request_ip,
	request_terms) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
`, status, id, instanceName, instanceIP, instanceUsername, instancePassword, instanceExpiry, requestDate, requestIP, requestTerms)
	if err != nil {
		return 0, err
	}

	instanceID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	return instanceID, nil
}

func dbRecordFeedback(id int64, feedback Feedback) error {
	// Get the feedback.
	feedbackId, _, _, _, _, err := dbGetFeedback(id)
	if err != nil {
		return err
	}

	if feedbackId == -1 {
		// Record new feedback.
		_, err := db.Exec(`
INSERT INTO feedback (
	session_id,
	rating,
	email,
	email_use,
	feedback) VALUES (?, ?, ?, ?, ?);
`, id, feedback.Rating, feedback.Email, feedback.EmailUse, feedback.Message)
		if err != nil {
			return err
		}

		return nil
	}

	// Update existing feedback.
	_, err = db.Exec(`
UPDATE feedback SET rating=?, email=?, email_use=?, feedback=? WHERE session_id=?;
`, feedback.Rating, feedback.Email, feedback.EmailUse, feedback.Message, id)
	if err != nil {
		return err
	}

	return nil
}

func dbDelete(id int64) error {
	_, err := db.Exec("DELETE FROM sessions WHERE id=?;", id)
	return err
}

func dbExpire(id int64) error {
	_, err := db.Exec("UPDATE sessions SET status=1 WHERE id=?;", id)
	return err
}

func dbIsAllocated(id int64) bool {
	var count int

	statement := `SELECT COUNT(id) FROM sessions WHERE status=2 AND id=?;`
	err := db.QueryRow(statement, id).Scan(&count)
	if err != nil {
		return false
	}

	return count == 1
}

func dbGetAllocated(instanceExpiry int64, requestDate int64, requestIP string, requestTerms string) (int64, string, string, string, string, string, error) {
	var id int64
	var uuid string
	var instanceName string
	var instanceIP string
	var instanceUsername string
	var instancePassword string

	// Check if feature is enabled at all.
	if config.Instance.Allocate.Count == 0 {
		return 0, "", "", "", "", "", fmt.Errorf("Pre-allocated instances isn't enabled")
	}

	// Find oldest pre-allocated instance.
	statement := `SELECT id, uuid, instance_name, instance_ip, instance_username, instance_password FROM sessions WHERE status=2 ORDER BY instance_expiry ASC LIMIT 1;`
	err := db.QueryRow(statement, id).Scan(&id, &uuid, &instanceName, &instanceIP, &instanceUsername, &instancePassword)
	if err != nil {
		return 0, "", "", "", "", "", err
	}

	// No pre-allocated instances available.
	if id == 0 {
		return 0, "", "", "", "", "", fmt.Errorf("No available pre-allocated instances")
	}

	// Update the record to match the new request.
	_, err = db.Exec("UPDATE sessions SET status=0, instance_expiry=?, request_date=?, request_ip=?, request_terms=? WHERE id=?", instanceExpiry, requestDate, requestIP, requestTerms, id)
	if err != nil {
		return 0, "", "", "", "", "", err
	}

	return id, uuid, instanceName, instanceIP, instanceUsername, instancePassword, nil
}

func dbActiveCount() (int, error) {
	var count int

	statement := `SELECT count(*) FROM sessions WHERE status=0;`
	err := db.QueryRow(statement).Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}

func dbActiveCountForIP(ip string) (int, error) {
	var count int

	statement := `SELECT count(*) FROM sessions WHERE status=0 AND request_ip=?;`
	err := db.QueryRow(statement, ip).Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}

func dbNextExpire() (int, error) {
	var expire int

	statement := `SELECT MIN(instance_expiry) FROM sessions WHERE status=0;`
	err := db.QueryRow(statement).Scan(&expire)
	if err != nil {
		return 0, err
	}

	return expire, nil
}

func dbIsLockedError(err error) bool {
	if err == nil {
		return false
	}
	if err == sqlite3.ErrLocked || err == sqlite3.ErrBusy {
		return true
	}
	if err.Error() == "database is locked" {
		return true
	}
	return false
}

func dbIsNoMatchError(err error) bool {
	if err == nil {
		return false
	}
	if err.Error() == "sql: no rows in result set" {
		return true
	}
	return false
}

func dbQueryRowScan(db *sql.DB, q string, args []interface{}, outargs []interface{}) error {
	for {
		err := db.QueryRow(q, args...).Scan(outargs...)
		if err == nil {
			return nil
		}
		if dbIsNoMatchError(err) {
			return err
		}
		if !dbIsLockedError(err) {
			return err
		}
		time.Sleep(1 * time.Second)
	}
}

func dbQuery(db *sql.DB, q string, args ...interface{}) (*sql.Rows, error) {
	for {
		result, err := db.Query(q, args...)
		if err == nil {
			return result, nil
		}
		if !dbIsLockedError(err) {
			return nil, err
		}
		time.Sleep(1 * time.Second)
	}
}

func dbDoQueryScan(db *sql.DB, q string, args []interface{}, outargs []interface{}) ([][]interface{}, error) {
	rows, err := db.Query(q, args...)
	if err != nil {
		return [][]interface{}{}, err
	}
	defer rows.Close()
	result := [][]interface{}{}
	for rows.Next() {
		ptrargs := make([]interface{}, len(outargs))
		for i := range outargs {
			switch t := outargs[i].(type) {
			case string:
				str := ""
				ptrargs[i] = &str
			case int:
				integer := 0
				ptrargs[i] = &integer
			default:
				return [][]interface{}{}, fmt.Errorf("Bad interface type: %s\n", t)
			}
		}
		err = rows.Scan(ptrargs...)
		if err != nil {
			return [][]interface{}{}, err
		}
		newargs := make([]interface{}, len(outargs))
		for i := range ptrargs {
			switch t := outargs[i].(type) {
			case string:
				newargs[i] = *ptrargs[i].(*string)
			case int:
				newargs[i] = *ptrargs[i].(*int)
			default:
				return [][]interface{}{}, fmt.Errorf("Bad interface type: %s\n", t)
			}
		}
		result = append(result, newargs)
	}
	err = rows.Err()
	if err != nil {
		return [][]interface{}{}, err
	}
	return result, nil
}

func dbQueryScan(db *sql.DB, q string, inargs []interface{}, outfmt []interface{}) ([][]interface{}, error) {
	for {
		result, err := dbDoQueryScan(db, q, inargs, outfmt)
		if err == nil {
			return result, nil
		}
		if !dbIsLockedError(err) {
			return nil, err
		}
		time.Sleep(1 * time.Second)
	}
}
