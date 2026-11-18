# A real OpenSSH sshd for the end-to-end test (test/sshd/sshd_test.go): root may sign in with a
# password, as on an openccu-lite system with SSH switched on, with the system's cipher and MAC
# choices and its SFTP subsystem as a program of its own (the system's line is
# "Subsystem sftp /usr/libexec/sftp-server"). The test switches it to keys only and takes it away
# mid-session. sshd's wait for an address after failed sign-ins is switched off here - the tests
# sign in wrongly on purpose, all from one address - and switched on by the one test that is about
# it: the drop-in directory is read first, and sshd takes the first value it reads. Nothing here is a real system's configuration or key: the host keys are made when
# the image is built, and root's password is the test's.
FROM alpine:3.22
RUN apk add --no-cache openssh-server openssh-sftp-server \
 && ssh-keygen -A \
 && echo 'root:e2e-root-pass' | chpasswd \
 && printf '%s\n' \
      'Include /etc/ssh/sshd_config.d/*.conf' \
      'PermitRootLogin yes' \
      'AuthorizedKeysFile .ssh/authorized_keys' \
      'PermitEmptyPasswords no' \
      'Ciphers aes256-ctr,aes192-ctr,aes128-ctr' \
      'MACs hmac-sha2-256,hmac-sha2-512' \
      'Subsystem sftp /usr/lib/ssh/sftp-server' \
      'PerSourcePenalties no' \
      > /etc/ssh/sshd_config \
 && mkdir -p /etc/ssh/sshd_config.d
EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
