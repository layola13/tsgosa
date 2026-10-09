function f(): void {
  const s = "abc";
  for (const k in s) {
    console.log(k);
  }
}
f();
