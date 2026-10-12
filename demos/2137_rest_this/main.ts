class C { x: number; y: number; constructor(x: number, y: number) { this.x = x; this.y = y; } sum(): i32 { const { x, ...rest } = this; return x + rest.y; } }
function main(): i32 {
  const c = new C(3, 4);
  console.log(c.sum());
  return 0;
}
