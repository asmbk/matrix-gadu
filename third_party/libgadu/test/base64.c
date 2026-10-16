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

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "libgadu.h"

struct test_data
{
	const char *input;
	const char *output;
};

static const struct test_data test_data[] =
{
	{ "", "" },
	{ "a", "YQ==" },
	{ "ab", "YWI=" },
	{ "abc", "YWJj" },
	{ "user:pass", "dXNlcjpwYXNz" },
	{ "user:pas", "dXNlcjpwYXM=" },
	{ "\xff\xfe\x80", "//6A" },
};

int main(void)
{
	size_t i;
	int result = 0;

	for (i = 0; i < sizeof(test_data) / sizeof(test_data[0]); i++) {
		char *encoded = gg_base64_encode(test_data[i].input);
		char *decoded;

		if (encoded == NULL || strcmp(encoded, test_data[i].output) != 0) {
			printf("encode(\"%s\") = \"%s\", expected \"%s\"\n",
				test_data[i].input, encoded ? encoded : "(null)",
				test_data[i].output);
			result = 1;
		}

		decoded = gg_base64_decode(test_data[i].output);

		if (decoded == NULL || strcmp(decoded, test_data[i].input) != 0) {
			printf("decode(\"%s\") = \"%s\", expected \"%s\"\n",
				test_data[i].output, decoded ? decoded : "(null)",
				test_data[i].input);
			result = 1;
		}

		free(encoded);
		free(decoded);
	}

	return result;
}
