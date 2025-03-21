package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/mfzzf/LLM_Based_HoneyPot/config"
	"github.com/mfzzf/LLM_Based_HoneyPot/logger"
	"github.com/mfzzf/LLM_Based_HoneyPot/proxy"
)

func main() {
	// 命令行参数
	listenAddr := flag.String("listen", "", "代理服务器监听地址")
	targetAddr := flag.String("target", "", "Ollama服务地址")
	configFile := flag.String("config", "", "配置文件路径")
	flag.Parse()

	// 加载配置
	var cfg config.Config
	var err error

	if *configFile != "" {
		cfg, err = config.LoadConfig(*configFile)
		if err != nil {
			log.Fatalf("无法加载配置文件: %v", err)
		}
		log.Printf("已从 %s 加载配置", *configFile)
	} else {
		cfg = config.DefaultConfig()
		log.Println("使用默认配置")
	}

	// 命令行参数覆盖配置文件
	if *listenAddr != "" {
		cfg.ListenAddr = *listenAddr
	}
	if *targetAddr != "" {
		cfg.TargetAddr = *targetAddr
	}

	// 初始化日志模块
	loggerInstance, err := logger.NewELKLogger(cfg.ELK)
	if err != nil {
		log.Printf("警告: 无法初始化ELK日志: %v", err)
		log.Println("继续运行，但不会记录到ELK")
	}
	defer loggerInstance.Close()

	// 创建代理服务器
	proxyServer, err := proxy.NewOllamaProxy(cfg.ListenAddr, cfg.TargetAddr, loggerInstance)
	if err != nil {
		log.Fatalf("无法创建代理服务器: %v", err)
	}

	// 设置关闭信号处理
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Println("正在关闭服务器...")
		loggerInstance.Close()
		os.Exit(0)
	}()

	// 启动服务器
	fmt.Printf("启动Ollama代理服务器，监听于%s，转发至%s\n", cfg.ListenAddr, cfg.TargetAddr)
	if err := proxyServer.Start(); err != nil {
		log.Fatalf("服务器启动失败: %v", err)
	}
}
