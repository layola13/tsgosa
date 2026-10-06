function foo(): i32 {
  return 1;
}
function main(): i32 {
  try {
    foo();
  } finally {
    console.log(1);
  }
  console.log(2);
  return 0;
}
