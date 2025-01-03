import sys


def main():
    file = open("ditto.txt", "ab")
    try:
        while True:
            bf = sys.stdin.buffer.read(1)
            if not bf:
                break
            file.write(bf)
            file.flush()
        file.close()
    except KeyboardInterrupt:
        file.close()


if __name__ == "__main__":
    main()
