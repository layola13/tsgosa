function f(x: i32): i32 {
  try {
    if (x > 0) { return 10; }
    return 20;
  } finally {
    console.log(1);
  }
}
function main(): i32 {
  console.log(f(5));
  return 0;
}
