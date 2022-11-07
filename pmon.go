package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ebfe/procevents"
	_ "github.com/mattn/go-sqlite3"
)

var pattern = ""

type Process struct {
	db_id      int64
	pid        int32
	name       string
	args       string
	start_time time.Time
	exit_code  uint32
}

var db *sql.DB
var bootTime int64
var processes = map[int32]Process{}

func initDB() {
	var err error
	db, err = sql.Open("sqlite3", "./process.db")
	if err != nil {
		log.Fatal(err)
	}
	sql := `CREATE TABLE IF NOT EXISTS tbProcess (id INTEGER PRIMARY KEY AUTOINCREMENT, pid INTEGER, process_name VARCHAR(500), args VARCHAR(500), start_time DATETIME, end_time DATETIME NULL, exit_code int NULL, signal int NULL);`
	db.Exec(sql)
	sql = `CREATE UNIQUE INDEX IF NOT EXISTS UI_Process_PID_ST ON  tbProcess (pid, start_time);`
	db.Exec(sql)
}

func getCommandLine(pid int32) (string, string, string) {

	env, _ := ioutil.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))

	buf, err := ioutil.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return "<unknown>", "", ""
	}
	var cmdline []string
	tok := bytes.Split(buf, []byte{0})
	for _, t := range tok {
		cmdline = append(cmdline, string(t))
	}
	if len(tok) == 1 {
		return cmdline[0], "", ""
	}
	return cmdline[0], strings.Join(cmdline[1:], " "), string(env)
}

func getBootTime() int64 {
	inFile, _ := os.Open("/proc/stat")
	defer inFile.Close()
	scanner := bufio.NewScanner(inFile)
	scanner.Split(bufio.ScanLines)

	var bootTime int64
	for scanner.Scan() {
		n, _ := fmt.Sscanf(scanner.Text(), "btime %d", &bootTime)
		if n == 1 {
			return bootTime
		}
	}
	return 0
}

func getStartTime(pid int32) int64 {
	buf, err := ioutil.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return -1
	}
	var stat = bytes.Split(buf, []byte{' '})
	startTime, _ := strconv.ParseInt(string(stat[21]), 10, 64)
	return startTime
}

func onProcessExec(pid int32) {
	process := Process{pid: pid}
	var env string
	process.name, process.args, env = getCommandLine(pid)
	if len(pattern) == 0 || strings.Contains(process.name, pattern) {
		log.Println("PID:", pid)
		process.start_time = time.Unix(bootTime+getStartTime(pid)/100, 0)
		sql := `REPLACE INTO tbProcess(pid, process_name, args, start_time) VALUES(?, ?, ?, ?)`
		ret, err := db.Exec(sql, process.pid, process.name, process.args, process.start_time)
		if err != nil {
			log.Printf("err: %s\n", err)
		} else {
			process.db_id, _ = ret.LastInsertId()
			processes[process.pid] = process
		}
		log.Printf("%v\n", process)
		log.Printf("%v\n", env)
	}
}

func onProcessExit(pid int32, code uint32, signal uint32) {
	if process, ok := processes[pid]; ok {
		sql := `UPDATE tbProcess SET end_time=datetime('now', 'localtime'), exit_code=?, signal=? WHERE id=?`
		_, err := db.Exec(sql, code, signal, process.db_id)
		if err != nil {
			log.Printf("err: %s\n", err)
		}

		log.Printf("exit: %s %d (%d)\n", process.name, pid, code)
		delete(processes, pid)
	}
}

func scanRunningProcess() {
	files, _ := ioutil.ReadDir("/proc/")
	for _, file := range files {
		if file.IsDir() {
			pid, err := strconv.Atoi(file.Name())
			if err == nil {
				onProcessExec(int32(pid))
			}
		}
	}
}

func main() {
	if len(os.Args) > 1 {
		pattern = os.Args[len(os.Args)-1]
	}
	initDB()
	defer db.Close()
	bootTime = getBootTime()
	scanRunningProcess()
	log.Printf("Process monitor started.")

	conn, err := procevents.Dial()
	if err != nil {
		log.Printf("err: %s\n", err)
		return
	}
	defer conn.Close()

	for {
		events, err := conn.Read()
		if err != nil {
			// log.Printf("err: %s\n", err)
			continue
		}
		for _, ev := range events {
			switch ev := ev.(type) {
			case procevents.Exec:
				onProcessExec(ev.Pid())
			case procevents.Exit:
				onProcessExit(ev.Pid(), ev.Code, ev.Signal)
			default:
			}
		}
	}
}
