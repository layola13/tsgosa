class C {
  static x: i32 = 1;
  static y: i32 = C.x + 10;
}
function main(): i32 {
  const c = new C();
  console.log(c.y);
  return 0;
}
