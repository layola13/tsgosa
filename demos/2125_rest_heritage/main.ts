class B { x: number; constructor(x: number) { this.x = x; } }
class D extends B { y: number; constructor(x: number, y: number) { super(x); this.y = y; } }
function main(): i32 {
  const d = new D(1, 2);
  const { x, ...rest } = d;
  console.log(x + rest.y);
  return 0;
}
