/*
 *  This program is free software; you can redistribute it and/or modify
 *  it under the terms of the GNU Lesser General Public License Version
 *  2.1 as published by the Free Software Foundation.
 *
 *  This program is distributed in the hope that it will be useful,
 *  but WITHOUT ANY WARRANTY; without even the implied warranty of
 *  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 *  GNU Lesser General Public License for more details.
 *
 *  You should have received a copy of the GNU Lesser General Public
 *  License along with this program; if not, write to the Free Software
 *  Foundation, Inc., 59 Temple Place, Suite 330, Boston, MA 02111-1307,
 *  USA.
 */

/*
 * Fills the TLS send buffer of an async session, then flushes the queue through gg_watch_fd and checks that the
 * peer decrypts exactly the bytes written.
 */

#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <pthread.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

#include <gnutls/gnutls.h>

#include "internal.h"
#include "libgadu.h"
#include "session.h"

#define DATA_SIZE (1024 * 1024)
#define SOCKET_BUFFER_SIZE 4096
#define TIMEOUT_SECONDS 20

static unsigned char data[DATA_SIZE];
static int server_fd;
static int start_reading_pipe[2];
static gnutls_certificate_credentials_t server_cred;
static gnutls_session_t server_session;

static void timeout_handler(int signum)
{
	(void) signum;
	static const char msg[] = "timeout - gg_write() or the queue flush did not return\n";
	if (write(STDERR_FILENO, msg, sizeof(msg) - 1) < 0)
		_exit(1);
	_exit(1);
}

static void *server_func(void *arg)
{
	size_t received = 0;
	char c;

	(void) arg;

	if (read(start_reading_pipe[0], &c, 1) != 1) {
		fprintf(stderr, "server: start signal read failed\n");
		return (void *) 1;
	}

	while (received < DATA_SIZE) {
		unsigned char buf[16384];
		ssize_t res = gnutls_record_recv(server_session, buf, sizeof(buf));

		if (res == GNUTLS_E_INTERRUPTED || res == GNUTLS_E_AGAIN)
			continue;

		if (res <= 0) {
			fprintf(stderr, "server: gnutls_record_recv: %zd, %s (after %zu bytes)\n", res,
				gnutls_strerror(res), received);
			return (void *) 1;
		}

		if (received + res > DATA_SIZE || memcmp(buf, data + received, res) != 0) {
			fprintf(stderr, "server: data mismatch at offset %zu\n", received);
			return (void *) 1;
		}

		received += res;
	}

	return NULL;
}

static int set_socket_buffers(int fd)
{
	int size = SOCKET_BUFFER_SIZE;

	if (setsockopt(fd, SOL_SOCKET, SO_SNDBUF, &size, sizeof(size)) == -1)
		return -1;

	return setsockopt(fd, SOL_SOCKET, SO_RCVBUF, &size, sizeof(size));
}

static void *server_handshake_func(void *arg)
{
	int res;

	(void) arg;

	do {
		res = gnutls_handshake(server_session);
	} while (res < 0 && !gnutls_error_is_fatal(res));

	if (res < 0) {
		fprintf(stderr, "server: gnutls_handshake: %d, %s\n", res, gnutls_strerror(res));
		return (void *) 1;
	}

	return NULL;
}

static int init_server(void)
{
	const char *srcdir = getenv("srcdir");
	char pem[4096];
	int res;

	snprintf(pem, sizeof(pem), "%s/connect.pem", srcdir ? srcdir : ".");

	if ((res = gnutls_certificate_allocate_credentials(&server_cred)) != GNUTLS_E_SUCCESS ||
		(res = gnutls_certificate_set_x509_key_file(server_cred, pem, pem, GNUTLS_X509_FMT_PEM)) !=
			GNUTLS_E_SUCCESS ||
		(res = gnutls_init(&server_session, GNUTLS_SERVER)) != GNUTLS_E_SUCCESS ||
		(res = gnutls_set_default_priority(server_session)) != GNUTLS_E_SUCCESS ||
		(res = gnutls_credentials_set(server_session, GNUTLS_CRD_CERTIFICATE, server_cred)) != GNUTLS_E_SUCCESS)
	{
		fprintf(stderr, "server init: %d, %s\n", res, gnutls_strerror(res));
		return -1;
	}

	gnutls_transport_set_int(server_session, server_fd);

	return 0;
}

static struct gg_session *create_client_session(int fd)
{
	struct gg_session *sess = calloc(1, sizeof(struct gg_session));
	struct gg_session_private *p = calloc(1, sizeof(struct gg_session_private));

	if (sess == NULL || p == NULL)
		return NULL;

	sess->private_data = p;
	sess->fd = fd;
	sess->async = 1;
	sess->state = GG_STATE_CONNECTED;

	if (gg_session_init_ssl(sess) == -1)
		return NULL;

	return sess;
}

int main(void)
{
	struct gg_session *sess;
	pthread_t handshake_thread, server_thread;
	void *thread_res;
	int fds[2];
	int res, i;

	signal(SIGALRM, timeout_handler);
	signal(SIGPIPE, SIG_IGN);
	alarm(TIMEOUT_SECONDS);

	for (i = 0; i < DATA_SIZE; i++)
		data[i] = (unsigned char) (i * 7 + i / 251);

	if (socketpair(AF_UNIX, SOCK_STREAM, 0, fds) == -1 || pipe(start_reading_pipe) == -1) {
		perror("socketpair/pipe");
		return 1;
	}

	if (set_socket_buffers(fds[0]) == -1 || set_socket_buffers(fds[1]) == -1) {
		perror("setsockopt");
		return 1;
	}

	server_fd = fds[1];

	if (init_server() == -1)
		return 1;

	sess = create_client_session(fds[0]);
	if (sess == NULL) {
		fprintf(stderr, "client session init failed\n");
		return 1;
	}

	pthread_create(&handshake_thread, NULL, server_handshake_func, NULL);

	do {
		res = gnutls_handshake(GG_SESSION_GNUTLS(sess));
	} while (res < 0 && !gnutls_error_is_fatal(res));

	pthread_join(handshake_thread, &thread_res);

	if (res < 0 || thread_res != NULL) {
		fprintf(stderr, "client: gnutls_handshake: %d, %s\n", res, gnutls_strerror(res));
		return 1;
	}

	fcntl(fds[0], F_SETFL, fcntl(fds[0], F_GETFL) | O_NONBLOCK);

	res = gg_write(sess, (const char *) data, DATA_SIZE);
	if (res == -1) {
		fprintf(stderr, "gg_write failed: %s\n", strerror(errno));
		return 1;
	}

	if (sess->send_left == 0) {
		fprintf(stderr, "gg_write sent everything, the send buffer never filled - test is void\n");
		return 77;
	}

	pthread_create(&server_thread, NULL, server_func, NULL);

	if (write(start_reading_pipe[1], "x", 1) != 1) {
		perror("write");
		return 1;
	}

	while (sess->send_left > 0) {
		struct pollfd pfd = { fds[0], POLLOUT, 0 };
		struct gg_event *ge;

		if (poll(&pfd, 1, 1000) == -1 && errno != EINTR) {
			perror("poll");
			return 1;
		}

		ge = gg_watch_fd(sess);

		if (ge == NULL || ge->type == GG_EVENT_CONN_FAILED) {
			fprintf(stderr, "gg_watch_fd failed\n");
			return 1;
		}

		gg_event_free(ge);
	}

	pthread_join(server_thread, &thread_res);

	if (thread_res != NULL)
		return 1;

	gg_free_session(sess);
	gnutls_deinit(server_session);
	gnutls_certificate_free_credentials(server_cred);

	return 0;
}
