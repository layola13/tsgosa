class Math2 {
  static double(x: i32): i32 {
    return x * 2;
  }
}
function main(): i32 {
  console.log(Math2.double(21));
  return 0;
}
