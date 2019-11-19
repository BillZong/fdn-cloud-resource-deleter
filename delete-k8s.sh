#!/usr/bin/env bash

# 要求目标机器必须有`expect`及`Tcl`。

sysname=`uname`

# params analyzing
if [ ${sysname}='Darwin' ]; then
    # this shell only works on Mac and bash now.
    ARGS=`getopt h:n:u:p:P:s: $@`
elif [ ${sysname}='Linux' || ${sysname}='Unix' ]; then
    # this only works on linux/unix.
    ARGS=`getopt -o h:n:u:p:P:s: -l hosts:,names:,user:,password:,port:ssh-file: -- "$@"`
else
    echo "Windows not supported yet"
fi

if [ $? != 0 ]; then
    echo "Terminating..."
    exit 1
fi

#将规范化后的命令行参数分配至位置参数（$1,$2,...)
eval set -- "${ARGS}"

sep=","
while true
do
    case "${1}" in
        -h|--hosts)
            hosts="$2"
            result=$(echo $2 | grep ",")
    	    if [ "$result" != "" ]; then
    	        OLD_IFS=$IFS
    	        IFS=","
    	        hostsArr=($hosts)
    	        IFS=$OLD_IFS
    	    fi
            shift 2
            ;;
        -n|--names)
            names=$2
            result=$(echo $2 | grep ",")
            if [ "$result" != "" ]; then
                OLD_IFS="$IFS"
                IFS=","
                namesArr=($names)
                IFS="$OLD_IFS"
            fi
            shift 2
            ;;
        -u|--user)
            user=$2
            shift 2
            ;;
        -p|--password)
            password=$2
            shift 2
            ;;
        -P|--port)
            port=$2
            shift 2
            ;;
        -s|--ssh-file)
            sshFile=$2
            shift 2
            ;;
        --)
            shift;
            break;
            ;;
        *)
	    echo "Internal error!"
            exit 1
            ;;
    esac
done

if [ -z $user ]; then
    user="root"
fi

if [ -n "$sshFile" ]; then
    deleter_file="./deleter-key.sh"
    key=$sshFile
elif [ -n "$password" ]; then
    deleter_file="./deleter-pwd.sh"
    key=$password
else
    echo "no ssh key file and password, could not login"
    exit 1
fi

if [ -z $port ]; then
    port=22
fi

# 将所有节点的标签删除，并移除节点，删除所有节点的配置文件
if [ -n "$namesArr" ]; then
    for nodeName in ${namesArr[@]}; do
        kubectl label nodes $nodeName openwhisk-role- --overwrite && kubectl drain $nodeName --delete-local-data --ignore-daemonsets --grace-period 10 --force && kubectl delete node $nodeName
        $deleter_file $host $port $user $key
    done
else
    kubectl label nodes $names openwhisk-role- --overwrite && kubectl drain $names --delete-local-data --ignore-daemonsets --grace-period 10 --force && kubectl delete node $names
    $deleter_file $hosts $port $user $key
fi
