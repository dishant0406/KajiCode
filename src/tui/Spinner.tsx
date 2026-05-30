import React from 'react';
import { Text } from 'ink';
import Spinner from 'ink-spinner';

interface ThinkingSpinnerProps {
  label?: string;
}

export const ThinkingSpinner: React.FC<ThinkingSpinnerProps> = ({ label = 'thinking' }) => {
  return (
    <Text color="gray" dimColor>
      <Spinner type="dots" /> {label}...
    </Text>
  );
};
