function count(parts: string[], x: i32, y: i32): i32 {
  return x + y;
}
function main(): i32 {
  console.log(count`a${3}b${4}c`);
  return 0;
}
