#!/bin/bash
# 首次启动初始化 + 拉起 supervisord。幂等：已初始化的数据卷重启时只做校验。
set -euo pipefail

DATA=/data
CFG=$DATA/config
SECRETS=$CFG/secrets.env
log() { echo "[init] $*"; }

if ! mountpoint -q "$DATA"; then
    log "警告：/data 不是挂载点（没有 -v 数据卷），容器删除后所有数据都会丢失，且文件临时目录会落到容器根盘"
fi

mkdir -p "$CFG" "$DATA"/{mysql,redis,log,static/avatar} \
         "$DATA"/file_sync/{sync,quickshare,share_temp,temp,versions}

# ── 1. 随机凭据：只在首次生成，之后复用 ───────────────────────────
if [ ! -f "$SECRETS" ]; then
    log "首次启动：生成数据库 / Redis 凭据"
    (
        umask 077
        {
            echo "MYSQL_ROOT_PASSWORD=$(openssl rand -hex 16)"
            echo "MYSQL_APP_PASSWORD=$(openssl rand -hex 16)"
            echo "REDIS_PASSWORD=$(openssl rand -hex 16)"
        } > "$SECRETS"
    )
fi
set -a; . "$SECRETS"; set +a

# ── 2. 后端配置：不存在才从范例生成，已有的绝不覆盖 ───────────────────
if [ ! -f "$CFG/config.yaml" ]; then
    log "生成默认配置 $CFG/config.yaml"
    sed -e "s/__DB_PASSWORD__/$MYSQL_APP_PASSWORD/" \
        -e "s/__REDIS_PASSWORD__/$REDIS_PASSWORD/" \
        /opt/syc/config.template.yaml > "$CFG/config.yaml"
    chmod 600 "$CFG/config.yaml"
fi

# ── 3. MySQL：数据目录为空才初始化 ────────────────────────────────
mkdir -p /run/mysqld
chown -R mysql:mysql /run/mysqld "$DATA/mysql"
if [ ! -d "$DATA/mysql/mysql" ]; then
    log "初始化 MySQL 数据目录"
    mysqld --initialize-insecure --user=mysql --datadir="$DATA/mysql" >/dev/null 2>&1 \
        || { log "MySQL 初始化失败"; exit 1; }
    # 显式带 --datadir：初始化这步不依赖 /etc/mysql/conf.d 里的配置
    #（配置万一被 mysql 因权限等原因忽略，这里也必须能自举）
    mysqld --user=mysql --datadir="$DATA/mysql" --skip-networking --socket=/run/mysqld/init.sock \
        --pid-file=/run/mysqld/init.pid >/dev/null 2>&1 &
    init_pid=$!
    for _ in $(seq 1 60); do
        mysqladmin --socket=/run/mysqld/init.sock -uroot ping >/dev/null 2>&1 && break
        sleep 1
    done
    mysql --socket=/run/mysqld/init.sock -uroot <<SQL
CREATE DATABASE IF NOT EXISTS syncfile CHARACTER SET utf8mb4;
CREATE USER IF NOT EXISTS 'syncfile'@'127.0.0.1' IDENTIFIED BY '$MYSQL_APP_PASSWORD';
CREATE USER IF NOT EXISTS 'syncfile'@'localhost' IDENTIFIED BY '$MYSQL_APP_PASSWORD';
GRANT ALL PRIVILEGES ON syncfile.* TO 'syncfile'@'127.0.0.1';
GRANT ALL PRIVILEGES ON syncfile.* TO 'syncfile'@'localhost';
ALTER USER 'root'@'localhost' IDENTIFIED BY '$MYSQL_ROOT_PASSWORD';
FLUSH PRIVILEGES;
SQL
    mysqladmin --socket=/run/mysqld/init.sock -uroot -p"$MYSQL_ROOT_PASSWORD" shutdown
    wait "$init_pid" 2>/dev/null || true
    log "MySQL 初始化完成"
fi

# ── 4. Redis 配置：每次启动重新生成到 /run（密码不出现在进程参数里）──────
mkdir -p /run/syc "$DATA/redis"
chown -R redis:redis "$DATA/redis"
cat > /run/syc/redis.conf <<CONF
bind 127.0.0.1
port 6379
protected-mode yes
requirepass $REDIS_PASSWORD
dir $DATA/redis
appendonly yes
save 900 1
save 300 100
CONF
chown redis:redis /run/syc/redis.conf
chmod 600 /run/syc/redis.conf

log "启动服务"
exec /usr/bin/supervisord -c /etc/supervisor/supervisord.conf
