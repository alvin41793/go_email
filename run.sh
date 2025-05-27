#!/bin/bash

echo "===== Go程序编译运行 ====="
echo "【1】选择目标系统:"
echo "  1. Mac (darwin/amd64)"
echo "  2. Mac M系列芯片 (darwin/arm64)"
echo "  3. Linux (linux/amd64)"
echo "  4. Windows (windows/amd64)"
echo ""
echo "【2】选择运行环境:"
echo "  1. Debug环境"
echo "  2. Release环境"
echo "=========================="

# 选择目标系统
read -p "请选择目标系统(默认1 Mac): " os_choice
os_choice=${os_choice:-1}

# 选择运行环境
read -p "请选择运行环境(默认1 Debug): " env_choice
env_choice=${env_choice:-1}

output_dir="./"
# 确保输出目录存在
mkdir -p $output_dir

# 设置目标系统和架构
case $os_choice in
  1)
    os_name="mac"
    GOOS="darwin"
    GOARCH="amd64"
    suffix=""
    ;;
  2)
    os_name="mac_arm"
    GOOS="darwin"
    GOARCH="arm64"
    suffix=""
    ;;
  3)
    os_name="linux"
    GOOS="linux"
    GOARCH="amd64"
    suffix=""
    ;;
  4)
    os_name="windows"
    GOOS="windows"
    GOARCH="amd64"
    suffix=".exe"
    ;;
  *)
    echo "系统选择无效，使用默认选项(Mac)"
    os_name="mac"
    GOOS="darwin"
    GOARCH="amd64"
    suffix=""
    ;;
esac

# 设置环境
case $env_choice in
  1)
    env_name="debug"
    ;;
  2)
    env_name="release"
    ;;
  *)
    echo "环境选择无效，使用默认选项(Debug)"
    env_name="debug"
    ;;
esac

# 构建输出文件名
output_file="${output_dir}/app_${os_name}_${env_name}${suffix}"

echo "正在编译Go应用(${os_name}/${env_name})..."
GOOS=$GOOS GOARCH=$GOARCH go build -o $output_file

# 如果编译成功并且是当前系统可以运行的平台，则运行
if [ $? -eq 0 ]; then
    echo "编译完成: $output_file"
    
    # 检查是否可以在当前系统运行
    current_os=$(uname)
    can_run=false
    
    if [[ "$current_os" == "Darwin" && ("$GOOS" == "darwin") ]]; then
        # 在Mac上编译Mac程序
        can_run=true
    elif [[ "$current_os" == "Linux" && ("$GOOS" == "linux") ]]; then
        # 在Linux上编译Linux程序
        can_run=true
    fi
    
    # 如果可以运行且不是Windows程序
    if [ "$can_run" = true ] && [ "$GOOS" != "windows" ]; then
        read -p "是否立即运行程序? (y/n): " run_choice
        if [[ "$run_choice" == "y" || "$run_choice" == "Y" ]]; then
            echo "开始运行程序(${env_name}环境)..."
            $output_file -env $env_name
        else
            echo "已跳过运行程序"
        fi
    else
        echo "注意: 交叉编译的程序不能在当前系统直接运行"
    fi
else
    echo "编译失败!"
fi 