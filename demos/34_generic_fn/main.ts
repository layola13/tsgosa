function id<T>(x: T): T {
  return x;
}
function main(): i32 {
  console.log(id(41));
  console.log(id(1) + id(2));
  return 0;
}
