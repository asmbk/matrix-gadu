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

#include "deflate.h"

#define MAX_OUTPUT (16 * 1024 * 1024)

static int round_trip(size_t size, int expect_success)
{
	unsigned char *compressed;
	size_t compressed_len;
	char *input, *output;
	int ok;

	input = malloc(size + 1);
	if (input == NULL)
		return 0;

	memset(input, 'a', size);
	input[size] = 0;

	compressed = gg_deflate(input, &compressed_len);
	if (compressed == NULL) {
		fprintf(stderr, "gg_deflate failed for %zu bytes\n", size);
		free(input);
		return 0;
	}

	output = gg_inflate(compressed, compressed_len);

	if (expect_success)
		ok = output != NULL && strcmp(output, input) == 0;
	else
		ok = output == NULL;

	if (!ok)
		fprintf(stderr, "%zu bytes (%zu compressed): expected %s\n", size, compressed_len,
			expect_success ? "a round trip" : "a refusal");

	free(output);
	free(compressed);
	free(input);

	return ok;
}

int main(void)
{
	if (!round_trip(1000, 1))
		return 1;

	if (!round_trip(MAX_OUTPUT, 1))
		return 1;

	if (!round_trip(MAX_OUTPUT + 1, 0))
		return 1;

	if (!round_trip(64 * 1024 * 1024, 0))
		return 1;

	return 0;
}
