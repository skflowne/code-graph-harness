// Pinned Tier A fixture — geometry primitives. Line/character positions here are
// asserted by eval/tiera; do not reformat without updating expected.json.

export interface Shape {
  area(): number;
}

export class Circle implements Shape {
  constructor(public readonly radius: number) {}

  area(): number {
    return Math.PI * this.radius * this.radius;
  }
}

export class Rectangle implements Shape {
  constructor(
    public readonly width: number,
    public readonly height: number,
  ) {}

  area(): number {
    return this.width * this.height;
  }
}

export function totalArea(shapes: Shape[]): number {
  return shapes.reduce((sum, s) => sum + s.area(), 0);
}
