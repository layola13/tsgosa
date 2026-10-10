function f(x: i32): i32 { return x * 2; }
function main(): i32 {
  console.log(`v=${f(21)}!`);
  console.log(`${f(1) + f(2)}`);
  return 0;
}
