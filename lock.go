package redislock

import (
	"RedisLock/utils"
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/gomodule/redigo/redis"
)

const RedisLockKeyPrefix = "REDIS_LOCK_PREFIX_"

var ErrLockAcquiredByOthers = errors.New("lock is acquired by others")

var ErrNil = redis.ErrNil

func IsRetryableErr(err error) bool {
	return errors.Is(err, ErrLockAcquiredByOthers)
}

type RedisLock struct {
	LockOptions
	key    string
	token  string
	Client LockClient
	// 看门狗运作标识
	runningDog int32
	// 停止看门狗
	stopDog context.CancelFunc
}

func NewRedisLock(key string, client LockClient, opts ...LockOption) *RedisLock {
	r := RedisLock{
		key:    key,
		token:  utils.GetProcessAndGoroutineIDStr(),
		Client: client,
	}

	for _, opt := range opts {
		opt(&r.LockOptions)
	}

	repairLock(&r.LockOptions)
	return &r
}

func (r *RedisLock) Lock(ctx context.Context) (err error) {
	defer func() {
		if err != nil {
			return
		}

		r.watchDog(ctx)
	}()
	err = r.tryLock(ctx)
	if err == nil {
		return nil
	}
	// 如果是非阻塞的
	if !r.isBlock {
		return err
	}
	// 判断错误是否可以允许重试，不可允许的类型则直接返回错误
	if !IsRetryableErr(err) {
		return err
	}

	// 基于阻塞模式持续轮询取锁
	err = r.blockingLock(ctx)
	return nil
}

func (r *RedisLock) tryLock(ctx context.Context) error {
	reply, err := r.Client.SetNEX(ctx, r.getLockKey(), r.token, r.expireSeconds)
	if err != nil {
		return err
	}
	if reply != 1 {
		return fmt.Errorf("reply: %d, err: %w", reply, ErrLockAcquiredByOthers)
	}
	return nil
}

func (r *RedisLock) watchDog(ctx context.Context) {
	if !r.watchDogMode {
		return
	}
	// 确保只用一个看门狗在运行
	for !atomic.CompareAndSwapInt32(&r.runningDog, 0, 1) {

	}

	ctx, r.stopDog = context.WithCancel(ctx)
	go func() {
		defer func() {
			atomic.StoreInt32(&r.runningDog, 0)
		}()
		r.runWatchDog(ctx)
	}()
}

func (r *RedisLock) runWatchDog(ctx context.Context) {
	ticker := time.NewTicker(WatchDogWorkStepSeconds * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_ = r.DelayExpire(ctx, WatchDogWorkStepSeconds+5)
	}
}

func (r *RedisLock) DelayExpire(ctx context.Context, expireSeconds int64) error {
	keysAndArgs := []interface{}{r.getLockKey(), r.token, expireSeconds}
	reply, err := r.Client.Eval(ctx, LuaCheckAndExpireDistributionLock, 1, keysAndArgs)
	if err != nil {
		return err
	}

	if ret, _ := reply.(int64); ret != 1 {
		return errors.New("can not expire lock without ownership of lock")
	}
	return nil
}

func (r *RedisLock) blockingLock(ctx context.Context) error {
	// 阻塞模式等锁时间上限
	timeoutCh := time.After(time.Duration(r.blockWaitingSeconds) * time.Second)
	// 轮询 ticker，每隔 50 ms 尝试取锁一次
	ticker := time.NewTicker(time.Duration(50) * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		select {
		// ctx 终止了
		case <-ctx.Done():
			return fmt.Errorf("lock failed, ctx timeout, err: %w", ctx.Err())
			// 阻塞等锁达到上限时间
		case <-timeoutCh:
			return fmt.Errorf("block waiting time out, err: %w", ErrLockAcquiredByOthers)
		// 放行
		default:
		}

		// 尝试取锁
		err := r.tryLock(ctx)
		if err == nil {
			// 加锁成功，返回结果
			return nil
		}

		// 不可重试类型的错误，直接返回
		if !IsRetryableErr(err) {
			return err
		}
	}

	// 不可达
	return nil
}

// Unlock 解锁. 基于 lua 脚本实现操作原子性.
func (r *RedisLock) Unlock(ctx context.Context) error {
	defer func() {
		// 停止看门狗
		if r.stopDog != nil {
			r.stopDog()
		}
	}()

	keysAndArgs := []interface{}{r.getLockKey(), r.token}
	reply, err := r.Client.Eval(ctx, LuaCheckAndDeleteDistributionLock, 1, keysAndArgs)
	if err != nil {
		return err
	}

	if ret, _ := reply.(int64); ret != 1 {
		return errors.New("can not unlock without ownership of lock")
	}

	return nil
}

func (r *RedisLock) getLockKey() string {
	return RedisLockKeyPrefix + r.key
}
