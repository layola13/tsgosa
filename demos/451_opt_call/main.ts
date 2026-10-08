class C {
  m(): i32 {
    return 8;
  }
}
function g(): i32 {
  return 3;
}
function main(): i32 {
  const c = new C();
  console.log(c.m?.());
  console.log(g?.());
  return 0;
}
