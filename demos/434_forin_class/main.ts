class C {
  x: i32 = 1;
  y: i32 = 2;
  get(): i32 {
    return 1;
  }
}
function main(): i32 {
  const c = new C();
  let s = 0;
  for (const k in c) {
    s = s + 1;
  }
  console.log(s);
  return 0;
}
