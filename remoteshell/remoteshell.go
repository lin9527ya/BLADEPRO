// Package remoteshell 提供基于 Go 原生 SSH 客户端的远程命令执行与连通性测试。
//
// 为什么不 shell 出去调 ssh 命令：Windows 默认没有 ssh.exe，且密码认证需要
// 交互输入，无法在 agent 工具里安全使用。原生客户端同时支持密码与私钥。
package remoteshell

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Target 是一台 SSH 设备的连接参数。Port 为 0 时按 22 处理，User 为空时按 root。
type Target struct {
	Host     string
	Port     int
	User     string
	Password string
	KeyPath  string
}

// Run 在目标上执行一条命令，返回 stdout+stderr。ctx 的 deadline 会同时约束
// 建连与命令执行（通过连接 deadline 实现）。
func Run(ctx context.Context, t Target, command string) (string, error) {
	client, err := dial(ctx, t)
	if err != nil {
		return "", err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("创建 SSH 会话失败: %w", err)
	}
	defer session.Close()
	out, err := session.CombinedOutput(command)
	return string(out), err
}

// Test 验证主机可达、认证成功且能执行命令（echo 一个标记串）。
func Test(ctx context.Context, t Target) error {
	const marker = "BLADEPRO_SSH_OK"
	out, err := Run(ctx, t, "echo "+marker)
	if err != nil {
		return err
	}
	if !strings.Contains(out, marker) {
		return fmt.Errorf("连接成功但测试命令输出异常")
	}
	return nil
}

func dial(ctx context.Context, t Target) (*ssh.Client, error) {
	host := strings.TrimSpace(t.Host)
	if host == "" {
		return nil, fmt.Errorf("缺少主机地址")
	}
	port := t.Port
	if port <= 0 {
		port = 22
	}
	user := strings.TrimSpace(t.User)
	if user == "" {
		user = "root"
	}
	methods, err := authMethods(t)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            methods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	d := net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, friendlyDialErr(err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	cc, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, friendlyHandshakeErr(err)
	}
	return ssh.NewClient(cc, chans, reqs), nil
}

func authMethods(t Target) ([]ssh.AuthMethod, error) {
	methods := []ssh.AuthMethod{}
	if keyPath := strings.TrimSpace(t.KeyPath); keyPath != "" {
		keyData, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("读取私钥失败: %w", err)
		}
		signer, err := ssh.ParsePrivateKey(keyData)
		if err != nil {
			return nil, fmt.Errorf("解析私钥失败: %w", err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if t.Password != "" {
		methods = append(methods, ssh.Password(t.Password))
	}
	if len(methods) == 0 {
		return nil, fmt.Errorf("请填写密码或私钥路径（至少一种认证方式）")
	}
	return methods, nil
}

func friendlyDialErr(err error) error {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "refused"):
		return fmt.Errorf("连接被拒绝：端口未开放或 SSH 服务未运行")
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline"), strings.Contains(msg, "unreachable"):
		return fmt.Errorf("连接超时或不可达：主机/端口检查一下")
	}
	return fmt.Errorf("连接失败: %w", err)
}

func friendlyHandshakeErr(err error) error {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "unable to authenticate") {
		return fmt.Errorf("认证失败：用户名或密码/密钥不正确")
	}
	return friendlyDialErr(err)
}
