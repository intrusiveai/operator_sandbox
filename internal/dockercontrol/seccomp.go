//go:build linux || darwin

package dockercontrol

// The startup allowlist permits the fixed Python entrypoint/dynamic loader and
// installation of the irreversible guest live filter. No sockets, process/thread
// creation, mount/namespace changes, ptrace, BPF or perf interfaces are allowed.
// openat2 is needed by current Docker runtime startup before Python executes.
// The live filter must subsequently deny exec and constrain executable mappings.
// Qualification with the published Python image remains required.
var startupSeccomp = []byte(`{"defaultAction":"SCMP_ACT_ERRNO","defaultErrnoRet":1,"syscalls":[{"names":["read","write","readv","writev","pread64","pwrite64","close","close_range","lseek","fstat","newfstatat","stat","lstat","statx","statfs","fstatfs","open","openat","openat2","access","faccessat","faccessat2","readlink","readlinkat","getdents64","fcntl","ioctl","dup","dup2","dup3","poll","ppoll","select","pselect6","epoll_create1","epoll_ctl","epoll_wait","epoll_pwait","mmap","mprotect","munmap","mremap","madvise","brk","arch_prctl","set_tid_address","set_robust_list","rseq","futex","rt_sigaction","rt_sigprocmask","rt_sigreturn","rt_sigsuspend","sigaltstack","restart_syscall","getrandom","clock_gettime","clock_getres","clock_nanosleep","nanosleep","gettimeofday","time","times","getpid","getppid","gettid","getuid","geteuid","getgid","getegid","getgroups","uname","sysinfo","getcwd","chdir","fchdir","umask","getrlimit","prlimit64","getrusage","sched_getaffinity","sched_yield","prctl","seccomp","execve","exit","exit_group","mkdir","mkdirat","unlink","unlinkat","rename","renameat","renameat2","ftruncate","truncate","fsync","fdatasync","fchmod","chmod","fchmodat","utimensat"],"action":"SCMP_ACT_ALLOW"}]}`)
