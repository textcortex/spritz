import { AnimatedMarkdown } from 'flowtoken';
import 'flowtoken/dist/styles.css';

interface MarkdownProps {
  content: string;
  className?: string;
  streaming?: boolean;
}

export function Markdown({ content, className = '', streaming = false }: MarkdownProps) {
  return (
    <div className={`prose text-[14px] max-w-none dark:prose-invert flex flex-col gap-6 ${className}`}>
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
