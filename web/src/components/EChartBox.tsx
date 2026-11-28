import { useEffect, useRef } from 'react';
import * as echarts from 'echarts';

interface EChartBoxProps {
  option: echarts.EChartsOption;
  height?: number;
  dark: boolean;
  className?: string;
}

export function EChartBox({ option, height = 280, dark, className = '' }: EChartBoxProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const chartRef = useRef<echarts.ECharts | null>(null);

  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    if (chartRef.current) {
      chartRef.current.dispose();
    }
    const chart = echarts.init(el, dark ? 'dark' : undefined, { renderer: 'canvas' });
    chartRef.current = chart;
    const ro = new ResizeObserver(() => chart.resize());
    ro.observe(el);
    return () => {
      ro.disconnect();
      chart.dispose();
      chartRef.current = null;
    };
  }, [dark]);

  useEffect(() => {
    if (!chartRef.current) return;
    chartRef.current.setOption(
      {
        backgroundColor: 'transparent',
        animationDuration: 420,
        animationEasing: 'cubicOut',
        ...option,
      },
      { notMerge: true },
    );
  }, [option, dark]);

  return <div ref={containerRef} style={{ height }} className={`w-full ${className}`} />;
}
