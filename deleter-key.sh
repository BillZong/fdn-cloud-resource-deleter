#!/usr/bin/env expect

if {$argc < 4} {
send_user "usage: $argv0 host port user ssh_key_file_path"
exit
}

# 超时时间
set timeout 4

set host [lindex $argv 0]
set port [lindex $argv 1]
set user [lindex $argv 2]
set ssh_key_file_path [lindex $argv 3]

#spawn表示开启新expect会话进程
spawn ssh -i $ssh_key_file_path -p $port $user@$host

# 检测密钥方式连接和密码，没有会超时自动跳过
expect "yes/no" { send "yes\r"; exp_continue }

expect "$user@"

send "rm -f /etc/kubernetes/kubelet.conf && rm -f /etc/kubernetes/bootstrap-kubelet.conf && rm -f /etc/kubernetes/pki/ca.crt\r"
expect "$user@"

send {$(ps aux | grep kubelet | grep -v grep | awk '{print $2}' | while read pid; do kill -9 $pid; done)}
send "\r"
expect "$user@"

send "exit 0\r"
#expect eof表示结束expect和相应进程会话结束，如果用interact会保持停留在当前进程会话
expect eof
