// Pinned Tier A fixture — consumer of geometry.ts. Cross-file references to
// Circle, Rectangle, and totalArea are asserted by eval/tiera.

import { Circle, Rectangle, totalArea, Shape } from "./geometry";

const shapes: Shape[] = [
  new Circle(2),
  new Rectangle(3, 4),
  new Circle(1),
];

export function report(): number {
  const t = totalArea(shapes);
  console.log(`total area = ${t}`);
  return t;
}
