function main(): i32 {
  const x = 7;
  switch (true) {
    case x < 5: console.log(1); break;
    case x < 10: console.log(2); break;
    default: console.log(3);
  }
  return 0;
}
