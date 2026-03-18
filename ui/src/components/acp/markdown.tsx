import { AnimatedMarkdown } from 'flowtoken';
import 'flowtoken/dist/styles.css';

interface MarkdownProps {
  content: string;
  className?: string;
  streaming?: boolean;
}

export function Markdown({ content, className = '', streaming = false }: MarkdownProps) {
  return (
    <div className={`prose prose-sm max-w-none dark:prose-invert ${className}`}>
      <AnimatedMarkdown
        content={content}
        animation={streaming ? 'fadeIn' : null}
        animationDuration="0.25s"
        animationTimingFunction="ease-in-out"
        sep="diff"
      />
    </div>
  );
}
