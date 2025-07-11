# 邮件客户端并发访问解决方案

## 问题描述

当同一个邮件账号同时进行多个操作（如拉取邮件列表和获取邮件详情）时，由于IMAP协议的有状态特性，会导致以下问题：

1. **连接池共享冲突**：每个邮箱地址在连接池中只有一个连接实例
2. **IMAP状态混乱**：每个操作都需要执行`SELECT`命令选择文件夹，同时执行可能导致状态冲突
3. **命令响应错乱**：并发的FETCH命令可能导致响应混乱，出现"bad sequence"错误

## 解决方案

### 1. 操作级别互斥锁

为每个连接池中的连接添加了`operationMutex`互斥锁：

```go
type PooledConnection struct {
    Client      *client.Client
    LastUsed    time.Time
    AccountInfo *EmailConfigInfo
    mutex       sync.Mutex
    // 新增：连接操作互斥锁，防止并发操作冲突
    operationMutex sync.Mutex
}
```

### 2. 加锁连接获取

提供了`GetConnectionWithLock`方法，确保获取连接时自动加锁：

```go
func (p *ConnectionPool) GetConnectionWithLock(config *EmailConfigInfo) (*client.Client, func(), error) {
    conn, err := p.GetConnection(config)
    if err != nil {
        return nil, nil, err
    }
    
    // 获取操作互斥锁
    pooledConn.operationMutex.Lock()
    
    // 返回解锁函数
    unlockFunc := func() {
        pooledConn.operationMutex.Unlock()
    }
    
    return conn, unlockFunc, nil
}
```

### 3. 自动锁管理

所有IMAP操作方法都使用`defer unlock()`确保自动释放锁：

```go
func (m *MailClient) tryListEmails(folder string, limit int, fromUID ...uint32) ([]EmailInfo, error) {
    // 连接IMAP服务器并加锁
    c, unlock, err := m.ConnectIMAPWithLock()
    if err != nil {
        return nil, err
    }
    defer unlock() // 确保在函数结束时释放锁
    
    // 执行IMAP操作...
}
```

## 已更新的方法

以下方法已更新为使用加锁连接：

1. `tryListEmails` - 获取邮件列表
2. `tryListEmailsFromUID` - 获取指定UID之后的邮件
3. `tryGetEmailContent` - 获取邮件详情
4. `tryGetAttachment` - 获取附件
5. `tryForwardOriginalEmail` - 转发邮件

## 使用效果

### 修改前问题：
- 同一账号的多个操作可能同时使用同一个IMAP连接
- 导致连接状态混乱，出现"bad sequence"等错误
- 操作失败率较高

### 修改后效果：
- 同一账号的操作按顺序执行，避免状态冲突
- 大幅减少连接错误和重试次数
- 提高操作成功率和稳定性

## 性能考虑

1. **锁粒度**：操作级别的锁，不影响不同账号之间的并发
2. **锁时间**：锁只在单次操作期间持有，操作完成后立即释放
3. **死锁避免**：使用defer确保锁一定会被释放

## 监控建议

可以通过以下方式监控并发效果：

1. 查看连接池状态：`GET /api/v1/system/connection-pool`
2. 监控日志中的锁获取/释放信息
3. 统计操作成功率和重试次数

## 注意事项

1. 确保所有新增的IMAP操作都使用`ConnectIMAPWithLock()`
2. 避免在锁内执行耗时操作
3. 如果需要长时间操作，考虑分批处理 