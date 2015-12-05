#/bin/sh

set -e
HOST=http://governator.io
GET=`which wget`
OUT=/usr/local/bin/governator

init_system() {
    if test -x /lib/init/upstart-job; then
        echo -n "upstart"
        return
    fi
    if test -x /etc/systemd/system; then
        echo -n "systemd"
        return
    fi
} 

if test -z $GET; then
    GET=`which curl`
fi

if test -z $GET; then
    echo "no wget nor curl found - please install one of them" &>2
    exit 1
fi

$GET $HOST/get/releases/`uname -s|tr \[A-Z\] \[a-z\]`/`uname -m`/latest/governator -O $OUT
chmod +x $OUT
mkdir -p /etc/governator/services

case `init_system` in
    "upstart")
        $GET $HOST/contrib/upstart/governator.conf -O /etc/init/governator.conf
        service governator start
        ;;
    "systemd")
        $GET $HOST/contrib/systemd/governator.service -O /etc/systemd/system/governator.service
        systemctl start governator.service
        systemctl enable governator.service
        ;;
    "chkconfig")
        ;;
    *)
        echo "unknown init system - you'll have to add governator manually" &>2
        exit 1
    ;;
esac
