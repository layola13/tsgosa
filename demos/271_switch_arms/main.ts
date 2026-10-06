function main(): i32 {
  const x = 2;
  switch (x) {
    case 1:
      console.log(1);
      break;
    case 2:
      console.log(x + 10);
      break;
    default:
      console.log(0);
  }
  const ok = x == 2;
  if (ok) {
    console.log(100);
  } else if (x == 1) {
    console.log(200);
  } else {
    console.log(300);
  }
  return 0;
}
