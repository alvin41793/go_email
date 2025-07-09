# LastSyncTime 功能说明

## 概述

`last_sync_time` 字段用于记录每个邮箱账号的最后同步时间，解决了多账号同步时的公平调度问题。

## 功能特点

### 1. 公平调度
- 按照 `last_sync_time` 升序排列账号
- 优先处理最久未同步的账号
- 避免前面的账号被频繁处理，后面的账号长期等待

### 2. 自动更新
- 每次同步完成后自动更新 `last_sync_time`
- 即使没有新邮件，也会更新同步时间
- 确保调度算法的准确性

### 3. 数据库迁移
- 应用启动时自动检查并添加 `last_sync_time` 字段
- 兼容现有数据库结构
- 无需手动执行数据库迁移

## 数据库结构

```sql
ALTER TABLE prime_email_account 
ADD COLUMN last_sync_time DATETIME NULL COMMENT '最后同步时间' AFTER type;
```

## 代码变更

### 1. 模型结构更新

```go
type PrimeEmailAccount struct {
    ID           int        `json:"id" gorm:"primaryKey;autoIncrement"`
    Account      string     `json:"account" gorm:"type:varchar(255)"`
    Password     string     `json:"password" gorm:"type:varchar(255)"`
    Status       int        `json:"status"`
    Type         int        `json:"type"`
    LastSyncTime *time.Time `json:"last_sync_time" gorm:"type:datetime;comment:'最后同步时间'"`
    CreatedAt    time.Time  `json:"created_at"`
    UpdatedAt    time.Time  `json:"updated_at"`
}
```

### 2. 查询排序优化

```go
// 按last_sync_time升序排列，NULL值排在最前面（从未同步的账户优先）
func GetActiveAccount() ([]PrimeEmailAccount, error) {
    var account []PrimeEmailAccount
    result := db.DB().Where("status = ?", 1).Order("last_sync_time ASC NULLS FIRST").Find(&account)
    return account, result.Error
}
```

### 3. 同步时间更新

```go
// 更新账号的最后同步时间
func UpdateLastSyncTime(accountID int) error {
    now := time.Now()
    result := db.DB().Model(&PrimeEmailAccount{}).Where("id = ?", accountID).Update("last_sync_time", now)
    return result.Error
}
```

## 使用场景

### 场景1: 多账号同步
假设有20个邮箱账号，系统每次处理5个：

**旧版本问题:**
- 第1次: 处理账号1-5
- 第2次: 处理账号6-10  
- 第3次: 又处理账号1-5 (重复)
- 第4次: 处理账号11-15
- 账号16-20可能很久才轮到

**新版本解决方案:**
- 第1次: 处理账号1-5，更新last_sync_time
- 第2次: 处理账号6-10，更新last_sync_time
- 第3次: 处理账号11-15，更新last_sync_time
- 第4次: 处理账号16-20，更新last_sync_time
- 第5次: 处理账号1-5 (最早同步的)

### 场景2: 定时任务
定时任务会自动更新同步时间，确保下次调度时能正确排序。

## 部署说明

### 1. 自动迁移
应用启动时会自动执行数据库迁移，无需手动操作。

### 2. 手动迁移（可选）
如果需要手动执行迁移，可以使用以下SQL：

```sql
-- 检查字段是否存在
SELECT COUNT(*) 
FROM INFORMATION_SCHEMA.COLUMNS 
WHERE TABLE_NAME = 'prime_email_account' 
AND COLUMN_NAME = 'last_sync_time';

-- 添加字段
ALTER TABLE prime_email_account 
ADD COLUMN last_sync_time DATETIME NULL COMMENT '最后同步时间' AFTER type;

-- 创建索引（可选）
CREATE INDEX idx_last_sync_time ON prime_email_account(last_sync_time);
```

### 3. 验证部署
查看应用日志，确认迁移成功：

```
开始执行数据库自动迁移...
正在添加last_sync_time字段...
last_sync_time字段添加成功
数据库自动迁移完成
```

## 注意事项

1. **字段类型**: `last_sync_time` 为 `DATETIME` 类型，可以为 `NULL`
2. **初始值**: 新账号或从未同步的账号该字段为 `NULL`，会被优先处理
3. **时区**: 使用服务器本地时间，确保时间一致性
4. **并发安全**: 使用事务确保时间更新的原子性

## 监控建议

1. 监控各账号的 `last_sync_time` 差异
2. 检查是否有账号长期未同步
3. 观察同步任务的分布是否均匀

## 故障排除

### 问题1: 字段添加失败
- 检查数据库权限
- 验证表名和字段名是否正确
- 查看数据库错误日志

### 问题2: 时间更新失败
- 检查事务是否正确提交
- 验证账号ID是否有效
- 查看应用日志中的错误信息

### 问题3: 排序不正确
- 检查 `ORDER BY` 语句是否包含 `NULLS FIRST`
- 验证数据库是否支持该语法
- 确认时间字段值是否正确更新 