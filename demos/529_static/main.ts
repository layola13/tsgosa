class C { static n: i32 = 7; static f(): i32 { return 3; } }
function main(): i32 {
  console.log(C.n);
  console.log(C.f());
  return 0;
}
