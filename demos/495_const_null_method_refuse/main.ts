class C {
  x: i32 = 5;
  m(): i32 {
    return this.x + 1;
  }
}
function main(): i32 {
  const c: C | null = null;
  console.log(c.m());
  return 0;
}
