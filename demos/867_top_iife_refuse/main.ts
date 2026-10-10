function add(a: i32): (b: i32) => i32 {
  return (b: i32) => a + b;
}
function m(): i32 {
  console.log(((x: i32) => x)(1, 2));
  return 0;
}
function c(): i32 {
  console.log(add(1)(2));
  return 0;
}
function s(): i32 {
  console.log((() => "hi")());
  return 0;
}
function main(): i32 {
  return m() + c() + s();
}
