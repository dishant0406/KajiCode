import React from 'react';
import { Box, Text } from 'ink';

export const Logo: React.FC = () => {
  return (
    <Box flexDirection="column" marginBottom={1}>
      <Text color="cyanBright" bold>
        {`   ███████╗ ███████╗ ██████╗   ██████╗ `}
      </Text>
      <Text color="cyanBright" bold>
        {`   ╚══███╔╝ ██╔════╝ ██╔══██╗ ██╔═══██╗`}
      </Text>
      <Text color="cyanBright" bold>
        {`     ███╔╝  █████╗   ██████╔╝ ██║   ██║`}
      </Text>
      <Text color="cyanBright" bold>
        {`    ███╔╝   ██╔══╝   ██╔══██╗ ██║   ██║`}
      </Text>
      <Text color="cyanBright" bold>
        {`   ███████╗ ███████╗ ██║  ██║ ╚██████╔╝`}
      </Text>
      <Text color="cyanBright" bold>
        {`   ╚══════╝ ╚══════╝ ╚═╝  ╚═╝  ╚═════╝ `}
      </Text>

      <Box marginTop={1}>
        <Text color="gray" dimColor>
          A clean terminal AI coding agent
        </Text>
      </Box>
    </Box>
  );
};
