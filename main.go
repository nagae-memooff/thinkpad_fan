package main

import (
	"github.com/nagae-memooff/config"
	//   sensors "github.com/nagae-memooff/config"
	"fmt"
	log "github.com/nagae-memooff/log4go"
	"io/ioutil"
	"os"
	"strconv"
	//   "strings"
	"time"

	"bufio"
	"net"
	"os/exec"
	"strings"
)

const (
	FULL_SPEED = iota
	CUSTOM
	AUTO
)

var (
	Log            log.Logger
	LogLevel       log.Level
	version        = "nagamemofan v0.0.2"
	contro_mode    = read_mode()
	monit_interval time.Duration
	sensors        = make(map[string]string)
	n              int
	listen_addr    string
	remote_addr    string
)

func init() {
	// 如果解析配置文件出现问题，直接异常退出
	err := config.Parse("fan.cfg")
	if err != nil {
		fmt.Println("FATAL ERROR: load config failed." + err.Error())
		os.Exit(1)
	}

	sensors, err = config.ParseToModel("sensors.cfg")
	if err != nil {
		fmt.Println("FATAL ERROR: load sensors failed." + err.Error())
		os.Exit(2)
	}

	//   fmt.Printf("config: %v, sensors: %v", config.GetModel(), sensors)

	LogLevel = log.LevelByString(config.Get("log_level"))
	Log = log.NewDefaultLogger(LogLevel)

	if config.Get("log_file") != "" {
		Log.AddFilter("file", LogLevel, log.NewFileLogWriter(config.Get("log_file"), false))
		fmt.Printf("print log to %s.\n", config.Get("log_file"))
	} else {
		fmt.Printf("print log to stdout.\n")
	}

	listen_addr = config.Get("listen_addr")
	if listen_addr == "" {
		listen_addr = "0.0.0.0:8899"
	}

}

func main() {
	args := os.Args
	if len(args) == 3 && args[1] == "-c" {
		cmd := []byte(args[2])
		remote_addr = config.Get("remote_addr")
		if remote_addr == "" {
			remote_addr = "192.168.1.11:8899"
		}

		conn, err := net.Dial("tcp", remote_addr)
		if err != nil {
			fmt.Printf("连接服务端失败: %s", err.Error())
			return
		}
		defer conn.Close()
		conn.Write(cmd)

		return
	}

	monit_interval = time.Duration(config.GetInt("monit_interval")) * time.Second

	// 最简单的逻辑： 如果温度大于75度，且mode=自动，则风扇全开;
	// 如果温度小于65度且mode=全开，则恢复自动控制
	Log.Info("启动风扇。")

	go listenAndServe()

	for {
		now_temp := read_temp()
		contro_mode = read_mode()

		Log.Info("当前温度： %d °C, 当前模式： %s", now_temp, mode_string())

		if now_temp > 75 && contro_mode == AUTO {
			Log.Info("温度高于临界，全转速。")
			contro_mode = FULL_SPEED
			_, err := set_mode(FULL_SPEED)
			if err != nil {
				Log.Error("修改模式出错：%s", err.Error())

			}
		} else if now_temp < 65 && contro_mode == FULL_SPEED {
			n++
			Log.Info("温度低于临界值 %d 次，继续观察。", n)
			if n > 4 {
				Log.Info("温度连续低于临界值 %d 次，自动控制转速。", n)
				contro_mode = AUTO
				_, err := set_mode(AUTO)
				if err != nil {
					Log.Error("修改模式出错：%s", err.Error())

				}
			}
		} else {
			n = 0
		}
		time.Sleep(monit_interval)
	}

}

// 风扇转速等级： 0～255
// 温度： 25 ～ 75

func read_mode() (mode int) {
	mode, err := strconv.Atoi(read_file(config.Get("mode_controller"), 1))
	if err != nil {
		Log.Error("读取当前控制模式失败： %s", err.Error())
		return AUTO
	}
	return
}

func mode_string() (mode string) {
	switch contro_mode {
	case 0:
		mode = "全速"
	case 1:
		mode = "手动"
	case 2:
		mode = "自动"
	default:
		mode = "未知"
	}

	return
}

func set_mode(mode int) (n int, err error) {
	mode_byte := []byte(strconv.Itoa(mode))

	filename := config.Get("mode_controller")

	ioutil.WriteFile(filename, mode_byte, os.ModeCharDevice)
	return
}

func read_temp() (temp int) {
	for _, sensor := range sensors {
		this_temp, err := strconv.Atoi(read_file(sensor, 5))
		if err != nil {
			Log.Error("读取温度信息失败！ 传感器路径： %s, 值: %s, err: %s", sensor, read_file(sensor, 5), err.Error())
			continue
		}

		this_temp /= 1000
		if this_temp > temp {
			temp = this_temp
		}
	}
	return
}

func read_speed() (speed int) {
	//TODO
	return
}

func set_speed(speed int) {
	//TODO
}

func read_file(filename string, n int) (out string) {

	_, err := os.Stat(filename)
	if err != nil {
		return
	}

	f, err := os.Open(filename)
	defer f.Close()
	if err != nil {
		return
	}

	buff := make([]byte, n)
	f.Read(buff)

	return string(buff)
}

func handleConn(conn net.Conn) {
	reader := bufio.NewReader(conn)
	line, _, err := reader.ReadLine()
	if err != nil {
		return
	}

	cmd := string(line)

	switch cmd {
	case "lock":
		sysexec("DISPLAY=:0 gnome-screensaver-command -l")
		Log.Info("收到指令: lock")
	case "unlock":
		sysexec("DISPLAY=:0 gnome-screensaver-command --exit")
		Log.Info("收到指令: unlock")
	default:
		Log.Error("不支持的指令： %s ", cmd)
		return
	}

	conn.Close()
}

func listenAndServe() {
	ln, err := net.Listen("tcp", listen_addr)
	if err != nil {
		Log.Error("listen error: %s", err)
		return
	}
	Log.Info("启动锁屏服务端口")

	for {
		conn, err := ln.Accept()
		if err != nil {
			Log.Error("accept error: %s", err)
			break
		}

		go handleConn(conn)
	}
}

func sysexec(cmd string, args ...string) (result string, err error) {
	arg := append([]string{cmd}, args...)
	arg_str := fmt.Sprintf("%s", strings.Join(arg, " "))

	ori_output, err := exec.Command("/bin/bash", "-l", "-c", arg_str).CombinedOutput()
	return string(ori_output), err
}

// 36  2000
// 72  2000
// 109 2450
// 145 2700
// 182 3000
// 218 3400
// 255 3400
