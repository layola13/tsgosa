class C { x: number; y: number; constructor(x: number, y: number) { this.x = x; this.y = y; } get(): i32 { return this.x + this.y; } }
function main(): i32 {
  const c = new C(1, 2);
  const { x, ...rest } = c;
  console.log(rest.get());
  return 0;
}
