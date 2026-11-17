# A real OpenSSH sshd for the end-to-end test (test/sshd/sshd_test.go): root may sign in with a
# password, as on an openccu-lite system with SSH switched on, with the system's cipher and MAC
# choices and its SFTP subsystem line. The test switches it to keys only and takes it away
# mid-session. Nothing here is a real system's configuration or key: the host keys are made when
# the image is built, and root's password is the test's.
FROM alpine:3.22
RUN apk add --no-cache openssh-server \
 && ssh-keygen -A \
 && echo 'root:e2e-root-pass' | chpasswd \
 && printf '%s\n' \
      'PermitRootLogin yes' \
      'AuthorizedKeysFile .ssh/authorized_keys' \
      'PermitEmptyPasswords no' \
      'Ciphers aes256-ctr,aes192-ctr,aes128-ctr' \
      'MACs hmac-sha2-256,hmac-sha2-512' \
      'Subsystem sftp internal-sftp' \
      'Include /etc/ssh/sshd_config.d/*.conf' \
      > /etc/ssh/sshd_config \
 && mkdir -p /etc/ssh/sshd_config.d
EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
