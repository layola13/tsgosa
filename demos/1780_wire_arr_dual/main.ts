class C { x: i32 = 0; y: i32 = 0; constructor(a: i32[]) { this.x = a[0]; this.y = a[1]; } }
function main(): i32 {
  const c = new C([3, 4]);
  console.log(c.x + c.y);
  return 0;
}
