class C { arr: i32[] = []; constructor(a: i32[]) { this.arr = a; } }
function main(): i32 {
  const c = new C([1, 2]);
  console.log(c.arr[1]);
  return 0;
}
