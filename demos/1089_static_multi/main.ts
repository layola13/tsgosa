class C {
  static A = 1;
  static B = 2;
  static {
    C.A = 10;
    C.B = 20;
  }
}
function main(): i32 {
  console.log(C.A + C.B);
  return 0;
}
