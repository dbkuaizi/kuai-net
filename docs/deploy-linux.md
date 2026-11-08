# Linux 部署

## 文件

| 文件 | 架构 | 用于 |
|---|---|---|
| `kuai-net-linux-amd64` | x86-64 | 绝大多数云服务器、VPS |
| `kuai-net-linux-arm64` | ARM64 | 阿里云/华为云 ARM 实例、树莓派 4/5、部分 NAS |

两个都是**静态链接**的单文件，不依赖 glibc，Alpine / Debian / Ubuntu / CentOS 都能直接跑。

不确定自己的机器是哪种：

```bash
uname -m
# x86_64        → 用 amd64
# aarch64/arm64 → 用 arm64
```

## 上传

```bash
scp kuai-net-linux-amd64 user@your-server:/opt/kuai-net
ssh user@your-server
```

## 运行

```bash
chmod +x /opt/kuai-net
/opt/kuai-net -addr :8080
```

浏览器访问 `http://服务器IP:8080`。

### 放后台常驻

**systemd**（推荐，Debian / Ubuntu / CentOS 通用）

```bash
sudo tee /etc/systemd/system/kuai-net.service > /dev/null <<'EOF'
[Unit]
Description=KuaiNet speed test
After=network.target

[Service]
Type=simple
ExecStart=/opt/kuai-net -addr :8080
Restart=on-failure
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now kuai-net
sudo systemctl status kuai-net
```

**nohup**（临时用）

```bash
nohup /opt/kuai-net -addr :8080 > /var/log/kuai-net.log 2>&1 &
```

**Docker**

```dockerfile
FROM alpine
COPY kuai-net-linux-amd64 /kuai-net
RUN chmod +x /kuai-net
EXPOSE 8080
CMD ["/kuai-net", "-addr", ":8080"]
```

## 放行端口

```bash
# ufw（Ubuntu/Debian）
sudo ufw allow 8080/tcp

# firewalld（CentOS/RHEL）
sudo firewall-cmd --add-port=8080/tcp --permanent && sudo firewall-cmd --reload
```

云服务器还需要在**控制台安全组**里放行 8080。

## 线上测速的几个前提

这几条比程序本身更重要，不满足的话测出来一定偏低：

1. **服务器上行带宽要够**
   测速时服务器的**出方向**带宽就是下载测试的上限。1 核 1M 的小机器只可能测出 1Mbps。

2. **关掉反向代理的缓冲**
   走 Nginx 时必须关掉缓冲，否则数据会被代理缓存，测出来是代理的速度而不是真实带宽：

   ```nginx
   location / {
       proxy_pass http://127.0.0.1:8080;
       proxy_buffering off;          # 关键
       proxy_request_buffering off;  # 上传同理
       proxy_http_version 1.1;
       proxy_set_header Connection "";
   }
   ```

3. **公网测速注意流量**
   10 秒多线程在千兆链路下会跑几个 GB。云服务器按流量计费的话先算一下成本，
   必要时缩短可选时长：

   ```bash
   /opt/kuai-net -addr :8080 -max-duration 15
   ```

4. **公网暴露建议加鉴权**
   程序本身没有登录。放到公网时建议在 Nginx 层加 Basic Auth 或限流，避免被人拿来刷流量。

## 参数

| 参数 | 默认值 | 说明 |
|---|---|---|
| `-addr` | `:8080` | 监听地址与端口 |
| `-max-duration` | `30` | 单次测速的最长秒数（与前端时长选项一致） |

## 排查

**页面打不开** — 先看服务是否在跑、端口是否监听：

```bash
ss -tlnp | grep 8080
curl -s localhost:8080/api/ping   # 应返回 ok
```

本机 curl 通、外面不通，基本就是防火墙或安全组。

**测速速度明显偏低** — 依次确认：服务器带宽上限、是否有 Nginx 缓冲、客户端所在网络的带宽、浏览器是否为现代内核。

**下载到一半中断** — 多为反向代理超时设置过早，或客户端网络中断。
