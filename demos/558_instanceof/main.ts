class C {
}
function main(): i32 {
  const c = new C();
  console.log(c instanceof C ? 1 : 0);
  return 0;
}
