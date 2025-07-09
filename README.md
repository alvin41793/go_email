# Node字段功能快速使用指南

## 快速开始

### 2. 分配账号到不同节点
```sql
-- 将账号1-10分配给节点1
UPDATE prime_email_account SET node = 1 WHERE id BETWEEN 1 AND 10;

-- 将账号11-20分配给节点2
UPDATE prime_email_account SET node = 2 WHERE id BETWEEN 11 AND 20;
```

### 3. 使用API同步特定节点
```bash
# 服务器1 - 同步节点1的账号
curl -X POST http://localhost:8080/api/sync-multiple-accounts \
  -H "Content-Type: application/json" \
  -d '{
    "max_workers": 3,
    "limit": 50,
    "node": 1
  }'

# 服务器2 - 同步节点2的账号
curl -X POST http://localhost:8080/api/sync-multiple-accounts \
  -H "Content-Type: application/json" \
  -d '{
    "max_workers": 3,
    "limit": 50,
    "node": 2
  }'
```

## 主要特性

✅ **多节点支持**: 支持多台服务器分布式处理邮箱账号  
✅ **灵活筛选**: 可以按节点筛选或处理全部账号  
✅ **公平调度**: 结合last_sync_time实现公平的账号调度  
✅ **自动迁移**: 提供自动数据库迁移工具  
✅ **向后兼容**: 兼容现有数据，默认节点为1  

## 部署架构

### 单服务器部署
```
服务器1 ← 处理所有账号 (node = 0 或不传入node)
```

### 双服务器部署
```
服务器1 ← 处理节点1的账号 (node = 1)
服务器2 ← 处理节点2的账号 (node = 2)
```

### 多服务器部署
```
服务器1 ← 处理节点1的账号 (node = 1)
服务器2 ← 处理节点2的账号 (node = 2)
服务器3 ← 处理节点3的账号 (node = 3)
服务器4 ← 处理节点4的账号 (node = 4)
```

## 文档链接

- [完整功能文档](docs/node_field_feature.md)
- [last_sync_time功能文档](docs/last_sync_time_feature.md)

## 注意事项

1. 新增字段默认值为1，兼容现有数据
2. 建议在生产环境前先在测试环境验证
3. 定期监控各节点的处理情况
4. 合理分配账号数量，避免单个节点过载 