function f(): void {
  const s = "hi";
  if (s ?? "d") {
    console.log(1);
  }
}
f();
