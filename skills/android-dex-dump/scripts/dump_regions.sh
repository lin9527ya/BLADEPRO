#!/system/bin/sh
# 内存区域转储 v2：支持指定 PID，上限提高到 128MB
PID=$1
if [ -z "$PID" ]; then echo "USAGE: dump_regions.sh PID"; exit 1; fi
OUT=/data/local/tmp/regions_$PID
rm -rf $OUT
mkdir -p $OUT
i=0
total=0
while IFS= read -r line; do
    start=$(echo "$line" | cut -d- -f1)
    rest=$(echo "$line" | cut -d- -f2-)
    end=$(echo "$rest" | cut -d' ' -f1)
    perms=$(echo "$rest" | cut -d' ' -f2)
    case "$perms" in
        r*) ;;
        *) continue ;;
    esac
    size=$(( 0x$end - 0x$start ))
    [ "$size" -lt 4096 ] && continue
    [ "$size" -gt 134217728 ] && continue
    path=$(echo "$rest" | cut -d' ' -f6-)
    case "$path" in
        /dev/*|/system/*|/apex/*|/vendor/*|/product/*|/data/app/*) continue ;;
    esac
    dd if=/proc/$PID/mem of=$OUT/r_$i.bin bs=4096 skip=$(( 0x$start / 4096 )) count=$(( size / 4096 )) 2>/dev/null
    if [ -s $OUT/r_$i.bin ]; then
        echo "$start $size r_$i.bin" >> $OUT/index.txt
        total=$(( total + 1 ))
    fi
    i=$(( i + 1 ))
done < /proc/$PID/maps
echo "PID=$PID DUMPED=$total"
