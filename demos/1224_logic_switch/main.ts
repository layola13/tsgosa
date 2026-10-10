function main(): i32 {
  const x = 2;
  switch (x && 3) {
    case 3: console.log(1); break;
    default: console.log(0);
  }
  return 0;
}
