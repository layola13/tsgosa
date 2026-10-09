function add(a: i32, b: i32): i32 {
  return a + b;
}
function main(): i32 {
  console.log((add)(40, 2));
  console.log(((add))(1, 2));
  return 0;
}
