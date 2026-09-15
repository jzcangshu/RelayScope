// Package payment 把"LDC 支付"抽象为可插拔端口：路由层只依赖 Provider 接口，
// 具体平台（LINUX DO Credit 易支付兼容协议等）作为独立实现插拔接入。
package payment

import (
	"context"
	"errors"
	"net/http"
	"time"
)

var ErrNotConfigured = errors.New("支付通道未配置")

type Status int

const (
	StatusPending Status = iota
	StatusPaid
	StatusFailed
)

// ChargeRequest 描述一笔待创建的支付：金额单位为 LDC（整数），Ref 为我方订单号。
type ChargeRequest struct {
	Ref         string
	AmountLDC   int64
	Description string
	NotifyURL   string
	ReturnURL   string
}

type Charge struct {
	Ref       string
	PayURL    string
	ExpiresAt time.Time
}

// Provider 是支付平台的统一端口。实现必须做到：
//   - CreateCharge 返回用户浏览器应跳转的支付页地址
//   - Verify 主动查询订单状态（回调丢失时的兜底）
//   - ParseCallback 校验平台异步通知的签名并解析订单号
type Provider interface {
	Configured() bool
	CreateCharge(ctx context.Context, req ChargeRequest) (Charge, error)
	Verify(ctx context.Context, ref string) (Status, error)
	ParseCallback(r *http.Request) (ref string, status Status, err error)
	// Refund 对已支付订单全额退回（争议处理用）。实现可返回 ErrNotConfigured。
	Refund(ctx context.Context, ref string, amountLDC int64) error
}

// Unconfigured 是未接入任何平台时的空实现：所有支付动作报 ErrNotConfigured。
type Unconfigured struct{}

func (Unconfigured) Configured() bool { return false }

func (Unconfigured) CreateCharge(context.Context, ChargeRequest) (Charge, error) {
	return Charge{}, ErrNotConfigured
}

func (Unconfigured) Verify(context.Context, string) (Status, error) {
	return StatusFailed, ErrNotConfigured
}

func (Unconfigured) ParseCallback(*http.Request) (string, Status, error) {
	return "", StatusFailed, ErrNotConfigured
}

func (Unconfigured) Refund(context.Context, string, int64) error { return ErrNotConfigured }
