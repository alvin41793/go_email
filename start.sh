#!/bin/bash

# 选择环境菜单
echo "请选择运行环境:"
echo "1. Debug环境（默认）"
echo "2. Release环境"
read -p "输入选择 [1-2]: " env_choice
env_choice=${env_choice:-1}

# 设置环境名称
case $env_choice in
    1)
        env_name="debug"
        ;;
    2)
        env_name="release"
        ;;
    *)
        echo "无效选择，使用默认环境(Debug)"
        env_name="debug"
        ;;
esac

# 检测系统类型
if [[ "$(uname)" == "Darwin" ]]; then
    if [[ "$(uname -m)" == "arm64" ]]; then
        app_file="./go_mail_mac_arm_${env_name}"
    else
        app_file="./go_mail_mac_${env_name}"
    fi
elif [[ "$(uname)" == "Linux" ]]; then
    app_file="./go_mail_linux_${env_name}"
else
    echo "不支持的系统"
    exit 1
fi

# 检查程序是否存在
if [ ! -f "$app_file" ]; then
    echo "找不到程序文件: $app_file"
    echo "请先运行 run.sh 编译程序"
    exit 1
fi

# 停止之前的程序实例
echo "停止现有程序实例..."
pkill -f "$app_file" || true
sleep 1

# 创建日志目录和文件
mkdir -p log
log_file="log/${env_name}_$(date +%Y%m%d_%H%M%S).log"

# 启动程序
echo "启动程序(${env_name}环境)，日志: $log_file"
nohup $app_file -env ${env_name} > $log_file 2>&1 &
echo "程序已启动，进程ID: $!" 