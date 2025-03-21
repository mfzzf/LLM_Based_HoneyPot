# Ollama 代理服务器

这是一个简单的 Ollama 代理服务器，可以转发发向 Ollama 的 API 请求。

## 功能特点

- 将请求转发到本地或远程的 Ollama 服务
- 支持命令行参数配置
- 支持配置文件
- 支持将请求和响应记录到 ELK (Elasticsearch, Logstash, Kibana) 系统

## 使用方法

### 直接运行

```bash
# 使用默认配置（监听 :8080，转发到 http://localhost:11434）
go run main.go

# 自定义监听地址和目标地址
go run main.go -listen :9000 -target http://ollama-server:11434
```

### 使用配置文件

创建 `config.json` 文件：

```json
{
  "listen_addr": ":8080",
  "target_addr": "http://localhost:11434",
  "log_enabled": true,
  "elk": {
    "enabled": true,
    "url": "http://elasticsearch:9200",
    "username": "elastic",
    "password": "changeme",
    "index": "ollama-proxy"
  }
}
```

然后运行：

```bash
go run main.go -config config.json
```

## ELK 日志配置

代理服务器支持将所有 Ollama 请求和响应记录到 Elasticsearch，配置项包括：

- `enabled`: 是否启用 ELK 日志
- `url`: Elasticsearch 服务器地址
- `username`: Elasticsearch 用户名（如果有）
- `password`: Elasticsearch 密码（如果有）
- `api_key`: Elasticsearch API Key（如果有，优先使用API Key而非用户名/密码）
- `index`: Elasticsearch 索引名称

### API Key 认证说明

如果同时提供了 API Key 和用户名/密码，系统将优先使用 API Key 进行认证。配置示例：

```json
{
  "elk": {
    "enabled": true,
    "url": "http://elasticsearch:9200",
    "api_key": "YOUR_API_KEY_HERE",
    "index": "ollama-proxy"
  }
}
```

您可以在 Elasticsearch 中生成 API Key，具体方法请参考 [Elasticsearch 文档](https://www.elastic.co/guide/en/elasticsearch/reference/current/security-api-create-api-key.html)。

## 准入控制

代理服务器支持内容准入控制，可以拦截不合规的请求，配置项包括：

- `enabled`: 是否启用准入控制
- `model_name`: 用于内容审核的Ollama模型
- `ollama_url`: 用于审核的Ollama服务地址
- `timeout_seconds`: 审核请求超时时间
- `max_retries`: 审核请求失败重试次数

准入控制会自动拦截包含制造炸药、武器或其他违规内容的请求，并返回友好的拒绝信息。

配置示例：

```json
"admission": {
  "enabled": true,
  "model_name": "phi3:latest",
  "ollama_url": "http://localhost:11434",
  "timeout_seconds": 5,
  "max_retries": 2
}
```

## 构建

```bash
go build -o ollama-proxy main.go
```

## 使用Docker

待添加
