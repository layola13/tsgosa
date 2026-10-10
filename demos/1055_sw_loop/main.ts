function main(): i32 {
  let t = 0;
  for (let i = 0; i < 4; i++) {
    switch (i % 2) {
      case 0: t += 10; break;
      default: t += 1;
    }
  }
  console.log(t);
  return 0;
}
