function main(): i32 {
  console.log(((x: i32) => ((y: i32) => x + y)(10))(5));
  return 0;
}
